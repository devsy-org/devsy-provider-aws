package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/log"
)

// ErrInstanceNotFound is returned when no matching EC2 instance exists.
var ErrInstanceNotFound = errors.New("instance not found")

// EC2 instance lifecycle states.
const (
	statePending      = "pending"
	stateRunning      = "running"
	stateShuttingDown = "shutting-down"
	stateStopped      = "stopped"
	stateStopping     = "stopping"
	stateTerminated   = "terminated"
)

func anyState() []string {
	return []string{
		statePending,
		stateRunning,
		stateShuttingDown,
		stateStopped,
		stateStopping,
	}
}

func GetDevsyInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	return GetMachine(ctx, cfg, name, anyState())
}

func GetDevsyStoppedInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	return GetMachine(ctx, cfg, name, []string{stateStopped})
}

func GetDevsyRunningInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	return GetMachine(ctx, cfg, name, []string{stateRunning})
}

func GetMachine(
	ctx context.Context,
	cfg aws.Config,
	name string,
	states []string,
) (Machine, error) {
	instance, err := GetInstance(ctx, cfg, name, states)
	if err != nil {
		return Machine{}, err
	}
	return NewMachineFromInstance(instance), nil
}

func GetInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
	states []string,
) (types.Instance, error) {
	svc := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devsy"),
				Values: []string{
					name,
				},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: states,
			},
		},
	}

	result, err := svc.DescribeInstances(ctx, input)
	if err != nil {
		return types.Instance{}, err
	}

	// Sort slice in order to have the newest result first
	sort.Slice(result.Reservations, func(i, j int) bool {
		return result.Reservations[i].Instances[0].LaunchTime.After(
			*result.Reservations[j].Instances[0].LaunchTime,
		)
	})

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return types.Instance{}, ErrInstanceNotFound
	}
	return result.Reservations[0].Instances[0], nil
}

func GetInstanceTags(providerAws *AwsProvider, zone route53Zone) []types.TagSpecification {
	tags := buildBaseTags(providerAws.Config.MachineID, zone)
	customTags := parseCustomTags(providerAws.Config.InstanceTags)
	tags = append(tags, customTags...)

	return []types.TagSpecification{{ResourceType: "instance", Tags: tags}}
}

func buildBaseTags(machineID string, zone route53Zone) []types.Tag {
	tags := []types.Tag{
		{Key: aws.String("Name"), Value: aws.String(machineID)},
		{Key: aws.String(tagKeyDevsy), Value: aws.String(machineID)},
	}

	if zone.id != "" {
		tags = append(tags, types.Tag{
			Key:   aws.String(tagKeyHostname),
			Value: aws.String(machineID + "." + zone.Name),
		})
	}

	return tags
}

func parseCustomTags(tagString string) []types.Tag {
	if tagString == "" {
		return nil
	}

	reg := regexp.MustCompile(
		`Name=([A-Za-z0-9!"#$%&'()*+\-./:;<>?@[\\\]^_{|}~]+),Value=([A-Za-z0-9!"#$%&'()*+\-./:;<>?@[\\\]^_{|}~]+)`,
	)
	tagList := reg.FindAllString(tagString, -1)
	if tagList == nil {
		return nil
	}

	tags := make([]types.Tag, 0, len(tagList))
	for _, tag := range tagList {
		tagSplit := strings.Split(tag, ",")
		name := strings.ReplaceAll(tagSplit[0], "Name=", "")
		value := strings.ReplaceAll(tagSplit[1], "Value=", "")
		tags = append(tags, types.Tag{Key: aws.String(name), Value: aws.String(value)})
	}

	return tags
}

func Start(ctx context.Context, provider *AwsProvider, instanceID string) error {
	log.Debugf("starting instance %s", instanceID)

	svc := ec2.NewFromConfig(provider.AwsConfig)

	input := &ec2.StartInstancesInput{
		InstanceIds: []string{
			instanceID,
		},
	}

	_, err := svc.StartInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	log.Debugf("instance %s started", instanceID)
	return nil
}

func Stop(ctx context.Context, provider *AwsProvider, instanceID string) error {
	log.Debugf("stopping instance %s", instanceID)

	svc := ec2.NewFromConfig(provider.AwsConfig)

	input := &ec2.StopInstancesInput{
		InstanceIds: []string{
			instanceID,
		},
	}

	_, err := svc.StopInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	log.Debugf("instance %s stopped", instanceID)
	return nil
}

func Status(ctx context.Context, provider *AwsProvider, name string) (client.Status, error) {
	log.Debugf("checking status for machine %s", name)

	result, err := GetDevsyInstance(ctx, provider.AwsConfig, name)
	if err != nil {
		if errors.Is(err, ErrInstanceNotFound) {
			return client.StatusNotFound, nil
		}
		return client.StatusNotFound, fmt.Errorf("get instance: %w", err)
	}

	status := result.Status
	var clientStatus client.Status
	switch status {
	case stateRunning:
		clientStatus = client.StatusRunning
	case stateStopped:
		clientStatus = client.StatusStopped
	case stateTerminated:
		log.Debugf("machine %s terminated", name)
		return client.StatusNotFound, nil
	default:
		clientStatus = client.StatusBusy
	}

	log.Debugf("machine %s status is %s", name, status)
	return clientStatus, nil
}

func Describe(ctx context.Context, provider *AwsProvider, name string) (string, error) {
	log.Debugf("describing machine %s", name)

	instance, err := GetInstance(ctx, provider.AwsConfig, name, anyState())
	if err != nil {
		if errors.Is(err, ErrInstanceNotFound) {
			return client.DescriptionNotFound, nil
		}
		return "", fmt.Errorf("describe instance: %w", err)
	}

	instanceBytes, err := json.MarshalIndent(instance, "", "  ") // #nosec G117
	if err != nil {
		return "", fmt.Errorf("marshal instance description: %w", err)
	}

	description := string(instanceBytes)

	log.Debugf("machine %s is %s", name, description)
	return description, nil
}

func terminateOnCleanup(provider *AwsProvider, instanceID string) {
	log.Debugf("terminating orphaned instance %s", instanceID)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	svc := ec2.NewFromConfig(provider.AwsConfig)
	_, err := svc.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		log.Warnf("failed to terminate orphaned instance %s: %v", instanceID, err)
	}
}

func Delete(ctx context.Context, provider *AwsProvider, machine Machine) error {
	log.Debugf("deleting instance %s", machine.InstanceID)

	svc := ec2.NewFromConfig(provider.AwsConfig)

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{
			machine.InstanceID,
		},
	}

	_, err := svc.TerminateInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("terminate instance: %w", err)
	}

	if machine.SpotInstanceRequestId != "" {
		_, err = svc.CancelSpotInstanceRequests(ctx, &ec2.CancelSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []string{
				machine.SpotInstanceRequestId,
			},
		})
		if err != nil {
			return fmt.Errorf("cancel spot request: %w", err)
		}
	}

	if provider.Config.UseRoute53Hostnames {
		r53Zone, err := GetDevsyRoute53Zone(ctx, provider)
		if err != nil {
			return fmt.Errorf("get Route53 zone: %w", err)
		}
		if r53Zone.id != "" {
			if err := DeleteDevsyRoute53Record(ctx, provider, r53Zone, machine); err != nil {
				return fmt.Errorf("delete Route53 record: %w", err)
			}
		}
	}

	log.Debugf("instance %s terminated", machine.InstanceID)
	return nil
}
