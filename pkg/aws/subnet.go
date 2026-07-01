package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy/pkg/log"
)

// subnetResult holds the resolved subnet ID and its VPC ID.
type subnetResult struct {
	subnetID         string
	vpcID            string
	availabilityZone string
}

func GetSubnet(ctx context.Context, provider *AwsProvider) (subnetResult, error) {
	log.Debugf("getting subnet: vpc=%s az=%s subnets=%v",
		provider.Config.VpcID, provider.Config.AvailabilityZone, provider.Config.SubnetIDs)

	if len(provider.Config.SubnetIDs) == 1 {
		return describeSubnetResult(ctx, provider, provider.Config.SubnetIDs[0])
	}

	if len(provider.Config.SubnetIDs) > 1 {
		return selectFromSpecifiedSubnets(ctx, provider)
	}

	return discoverSubnet(ctx, provider)
}

func selectFromSpecifiedSubnets(ctx context.Context, provider *AwsProvider) (subnetResult, error) {
	subnetIDs := provider.Config.SubnetIDs
	az := provider.Config.AvailabilityZone
	log.Debugf("selecting subnet from %d specified subnets", len(subnetIDs))

	svc := ec2.NewFromConfig(provider.AwsConfig)
	subnets, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: subnetIDs})
	if err != nil {
		return subnetResult{}, fmt.Errorf("list specified subnets %q: %w", subnetIDs, err)
	}
	if len(subnets.Subnets) == 0 {
		return subnetResult{}, fmt.Errorf("no subnets found with IDs %q", subnetIDs)
	}

	subnet := selectSubnetWithMostIPs(subnets.Subnets, az)
	if subnet == nil {
		if az == "" {
			return subnetResult{}, fmt.Errorf("no subnets found with IDs %q", subnetIDs)
		}
		return subnetResult{}, fmt.Errorf(
			"no subnets found with IDs %q in availability zone %q",
			subnetIDs,
			az,
		)
	}

	log.Debugf(
		"selected subnet %s with %d available IPs",
		*subnet.SubnetId,
		*subnet.AvailableIpAddressCount,
	)
	return subnetResultFrom(subnet), nil
}

func selectSubnetWithMostIPs(subnets []types.Subnet, az string) *types.Subnet {
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		s := subnets[i]
		if az != "" {
			if s.AvailabilityZone == nil || *s.AvailabilityZone != az {
				continue
			}
		}
		if s.AvailableIpAddressCount == nil {
			continue
		}
		if selected == nil || *s.AvailableIpAddressCount > maxIPCount {
			maxIPCount = *s.AvailableIpAddressCount
			selected = &subnets[i]
		}
	}
	return selected
}

func describeSubnetResult(
	ctx context.Context,
	provider *AwsProvider,
	subnetID string,
) (subnetResult, error) {
	svc := ec2.NewFromConfig(provider.AwsConfig)
	out, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	if err != nil {
		return subnetResult{}, fmt.Errorf("describe subnet %s: %w", subnetID, err)
	}
	if len(out.Subnets) == 0 {
		return subnetResult{}, fmt.Errorf("subnet %s not found", subnetID)
	}
	log.Debugf("using configured subnet %s (vpc: %s)", subnetID, *out.Subnets[0].VpcId)
	return subnetResultFrom(&out.Subnets[0]), nil
}

func subnetResultFrom(s *types.Subnet) subnetResult {
	r := subnetResult{subnetID: *s.SubnetId}
	if s.VpcId != nil {
		r.vpcID = *s.VpcId
	}
	if s.AvailabilityZone != nil {
		r.availabilityZone = *s.AvailabilityZone
	}
	return r
}

func discoverSubnet(ctx context.Context, provider *AwsProvider) (subnetResult, error) {
	vpcID := provider.Config.VpcID
	az := provider.Config.AvailabilityZone
	log.Debugf("searching for suitable subnet")

	svc := ec2.NewFromConfig(provider.AwsConfig)
	subnets, err := listAllSubnets(ctx, svc, az)
	if err != nil {
		return subnetResult{}, err
	}

	if subnet := findTaggedDevsySubnet(filterByVPC(subnets, vpcID)); subnet != nil {
		log.Debugf(
			"found tagged subnet %s with %d available IPs",
			*subnet.SubnetId,
			*subnet.AvailableIpAddressCount,
		)
		return subnetResultFrom(subnet), nil
	}

	if subnet := findVPCPublicSubnet(subnets, vpcID); subnet != nil {
		log.Debugf(
			"found VPC subnet %s with %d available IPs",
			*subnet.SubnetId,
			*subnet.AvailableIpAddressCount,
		)
		return subnetResultFrom(subnet), nil
	}

	if vpcID == "" {
		return subnetResult{}, fmt.Errorf(
			"could not find a suitable subnet. Please either specify a subnet ID or VPC ID, or tag the desired" +
				" subnets with devsy=devsy",
		)
	}

	return subnetResult{}, fmt.Errorf(
		"no suitable subnet found in VPC %q. Please specify a subnet ID or tag subnets with devsy=devsy",
		vpcID,
	)
}

func listAllSubnets(ctx context.Context, svc *ec2.Client, az string) ([]types.Subnet, error) {
	input := &ec2.DescribeSubnetsInput{}
	if az != "" {
		input.Filters = []types.Filter{
			{Name: aws.String("availability-zone"), Values: []string{az}},
		}
	}

	var subnets []types.Subnet
	p := ec2.NewDescribeSubnetsPaginator(svc, input)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list all subnets: %w", err)
		}
		subnets = append(subnets, page.Subnets...)
	}
	return subnets, nil
}

func filterByVPC(subnets []types.Subnet, vpcID string) []types.Subnet {
	if vpcID == "" {
		return subnets
	}
	var filtered []types.Subnet
	for _, s := range subnets {
		if s.VpcId != nil && *s.VpcId == vpcID {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func findTaggedDevsySubnet(subnets []types.Subnet) *types.Subnet {
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		if subnets[i].AvailableIpAddressCount == nil {
			continue
		}
		if isDevsyTagged(subnets[i].Tags) && *subnets[i].AvailableIpAddressCount > maxIPCount {
			maxIPCount = *subnets[i].AvailableIpAddressCount
			selected = &subnets[i]
		}
	}
	return selected
}

func isDevsyTagged(tags []types.Tag) bool {
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil &&
			*tag.Key == tagKeyDevsy && *tag.Value == tagKeyDevsy {
			return true
		}
	}
	return false
}

func findVPCPublicSubnet(subnets []types.Subnet, vpcID string) *types.Subnet {
	if vpcID == "" {
		return nil
	}
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		s := &subnets[i]
		if isPublicSubnetInVPC(s, vpcID) && *s.AvailableIpAddressCount > maxIPCount {
			maxIPCount = *s.AvailableIpAddressCount
			selected = s
		}
	}
	return selected
}

func isPublicSubnetInVPC(s *types.Subnet, vpcID string) bool {
	return s.VpcId != nil && s.MapPublicIpOnLaunch != nil &&
		s.AvailableIpAddressCount != nil &&
		*s.VpcId == vpcID && *s.MapPublicIpOnLaunch
}

func GetDevsyVPC(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.VpcID != "" {
		log.Debugf("using configured VPC %s", provider.Config.VpcID)
		return provider.Config.VpcID, nil
	}

	// Get a list of VPCs so we can associate the group with the first VPC.
	svc := ec2.NewFromConfig(provider.AwsConfig)

	result, err := svc.DescribeVpcs(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("describe VPCs: %w", err)
	}

	if len(result.Vpcs) == 0 {
		return "", fmt.Errorf("there are no VPCs to associate with")
	}

	// We need to find a default vpc
	for _, vpc := range result.Vpcs {
		if *vpc.IsDefault {
			log.Debugf("using default VPC %s", *vpc.VpcId)
			return *vpc.VpcId, nil
		}
	}

	return "", nil
}
