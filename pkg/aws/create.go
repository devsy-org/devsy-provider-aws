package aws

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy/pkg/log"
)

func Create(ctx context.Context, cfg aws.Config, providerAws *AwsProvider) (Machine, error) {
	opts := providerAws.Config
	log.Debugf("creating instance: machine=%s type=%s ami=%s disk=%dGB",
		opts.MachineID, opts.MachineType, opts.DiskImage, opts.DiskSizeGB)

	subnet, err := GetSubnet(ctx, providerAws)
	if err != nil {
		return Machine{}, fmt.Errorf("determine subnet ID: %w", err)
	}

	dataVolumeID, err := precreateDataVolume(ctx, cfg, providerAws, subnet.availabilityZone)
	if err != nil {
		return Machine{}, err
	}

	cleanupVolume := func() { deleteVolumeIfPresent(cfg, dataVolumeID) }

	instance, r53Zone, err := buildRunInstancesInput(ctx, providerAws, subnet)
	if err != nil {
		cleanupVolume()
		return Machine{}, err
	}

	svc := ec2.NewFromConfig(cfg)

	log.Debugf("launching EC2 instance")
	result, err := svc.RunInstances(ctx, instance)
	if err != nil {
		cleanupVolume()
		return Machine{}, err
	}
	log.Debugf("EC2 instance launched: %s", *result.Instances[0].InstanceId)

	instanceID := aws.ToString(result.Instances[0].InstanceId)

	terminate := func() {
		terminateOnCleanup(providerAws, instanceID)
		cleanupVolume()
	}

	if err := attachDataVolumeIfPresent(ctx, providerAws, instanceID, dataVolumeID); err != nil {
		terminate()
		return Machine{}, err
	}

	machine := NewMachineFromInstance(result.Instances[0])

	resolvedIP, err := upsertRoute53ForInstance(ctx, providerAws, r53Zone, result.Instances[0])
	if err != nil {
		terminate()
		return Machine{}, fmt.Errorf("create Route53 record: %w", err)
	}
	if resolvedIP != "" {
		machine.PublicIP = resolvedIP
	}

	log.Debugf("instance %s created", machine.InstanceID)
	return machine, nil
}

// precreateDataVolume creates the volume before launch so its ID can be embedded
// in the user-data script for NVMe device resolution. Returns "" if none configured.
func precreateDataVolume(
	ctx context.Context,
	cfg aws.Config,
	providerAws *AwsProvider,
	az string,
) (string, error) {
	if !providerAws.Config.HasDataVolume() {
		return "", nil
	}
	dataVolumeID, err := createDataVolume(ctx, cfg, providerAws, az)
	if err != nil {
		return "", err
	}
	providerAws.Config.DataVolumeID = dataVolumeID
	return dataVolumeID, nil
}

// attachDataVolumeIfPresent waits for the instance to be running, then attaches
// the pre-created volume. No-op if none configured.
func attachDataVolumeIfPresent(
	ctx context.Context,
	providerAws *AwsProvider,
	instanceID, dataVolumeID string,
) error {
	if dataVolumeID == "" {
		return nil
	}

	waiter := ec2.NewInstanceRunningWaiter(ec2.NewFromConfig(providerAws.AwsConfig))
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for instance %s to be running: %w", instanceID, err)
	}
	return attachDataVolume(ctx, providerAws, instanceID, dataVolumeID)
}

func buildRunInstancesInput(
	ctx context.Context,
	providerAws *AwsProvider,
	subnet subnetResult,
) (*ec2.RunInstancesInput, route53Zone, error) {
	devsySG, err := resolveSecurityGroups(ctx, providerAws, subnet.vpcID)
	if err != nil {
		return nil, route53Zone{}, err
	}
	userData, err := GetInjectKeypairScript(providerAws.Config)
	if err != nil {
		return nil, route53Zone{}, err
	}
	r53Zone, err := resolveRoute53Zone(ctx, providerAws)
	if err != nil {
		return nil, route53Zone{}, err
	}
	volSizeI32, err := validatedDiskSize(providerAws.Config.DiskSizeGB)
	if err != nil {
		return nil, route53Zone{}, err
	}
	cfg := providerAws.Config
	instance := &ec2.RunInstancesInput{
		ImageId:          aws.String(cfg.DiskImage),
		InstanceType:     types.InstanceType(cfg.MachineType),
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SecurityGroupIds: devsySG,
		SubnetId:         aws.String(subnet.subnetID),
		MetadataOptions: &types.InstanceMetadataOptionsRequest{
			HttpEndpoint:            types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:              types.HttpTokensStateRequired,
			HttpPutResponseHopLimit: aws.Int32(1),
		},
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: aws.String(cfg.RootDevice),
				Ebs: &types.EbsBlockDevice{
					VolumeSize: &volSizeI32,
				},
			},
		},
		TagSpecifications: GetInstanceTags(providerAws, r53Zone),
		UserData:          &userData,
	}

	applyNestedVirtualization(providerAws, instance)
	applySpotInstance(providerAws, instance)

	if err := applyInstanceProfile(ctx, providerAws, instance); err != nil {
		return nil, route53Zone{}, err
	}

	return instance, r53Zone, nil
}

func resolveSecurityGroups(
	ctx context.Context,
	p *AwsProvider,
	subnetVPC string,
) ([]string, error) {
	vpcID := subnetVPC
	if vpcID == "" {
		vpcID = p.Config.VpcID
	}
	return GetDevsySecurityGroups(ctx, p, vpcID)
}

func resolveRoute53Zone(ctx context.Context, p *AwsProvider) (route53Zone, error) {
	if !p.Config.UseRoute53Hostnames {
		return route53Zone{}, nil
	}
	return GetDevsyRoute53Zone(ctx, p)
}

func validatedDiskSize(size int) (int32, error) {
	if size < 0 || size > math.MaxInt32 {
		return 0, fmt.Errorf("invalid disk size: %d", size)
	}
	return int32(size), nil //nolint:gosec // bounds checked above
}

func applyNestedVirtualization(providerAws *AwsProvider, instance *ec2.RunInstancesInput) {
	if !providerAws.Config.UseNestedVirtualization {
		return
	}
	log.Debugf("enabling nested virtualization")
	instance.CpuOptions = &types.CpuOptionsRequest{
		NestedVirtualization: types.NestedVirtualizationSpecificationEnabled,
	}
}

func applySpotInstance(providerAws *AwsProvider, instance *ec2.RunInstancesInput) {
	if !providerAws.Config.UseSpotInstance {
		return
	}
	log.Debugf("using spot instance (type: %s)", providerAws.Config.SpotInstanceType)
	spotOpts := &types.SpotMarketOptions{
		SpotInstanceType: types.SpotInstanceType(providerAws.Config.SpotInstanceType),
	}
	if providerAws.Config.SpotInstanceType == "persistent" {
		spotOpts.InstanceInterruptionBehavior = "stop"
	}
	instance.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{
		MarketType:  "spot",
		SpotOptions: spotOpts,
	}
}

func upsertRoute53ForInstance(
	ctx context.Context,
	providerAws *AwsProvider,
	zone route53Zone,
	inst types.Instance,
) (string, error) {
	if zone.id == "" {
		return "", nil
	}
	hostname := providerAws.Config.MachineID + "." + zone.Name
	ip := *inst.PrivateIpAddress

	if !zone.private {
		svc := ec2.NewFromConfig(providerAws.AwsConfig)

		publicIP, err := resolvePublicIP(ctx, svc, inst)
		if err != nil {
			return "", err
		}

		ip = publicIP
	}

	log.Debugf("creating Route53 record: %s -> %s", hostname, ip)

	if err := UpsertDevsyRoute53Record(ctx, providerAws, route53Record{
		zoneID:   zone.id,
		hostname: hostname,
		ip:       ip,
	}); err != nil {
		return "", err
	}

	return ip, nil
}

func resolvePublicIP(
	ctx context.Context,
	svc *ec2.Client,
	inst types.Instance,
) (string, error) {
	if inst.PublicIpAddress != nil {
		return *inst.PublicIpAddress, nil
	}

	instanceID := *inst.InstanceId
	log.Debugf("waiting for public IP on instance %s", instanceID)

	waiter := ec2.NewInstanceRunningWaiter(svc)

	descOut, err := waiter.WaitForOutput(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 5*time.Minute)
	if err != nil {
		return "", fmt.Errorf("wait for instance running: %w", err)
	}

	if descOut.Reservations[0].Instances[0].PublicIpAddress == nil {
		return "", fmt.Errorf("instance %s has no public IP for public Route53 zone", instanceID)
	}

	return *descOut.Reservations[0].Instances[0].PublicIpAddress, nil
}
