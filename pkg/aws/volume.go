package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy/pkg/log"
)

// createDataVolume creates a standalone EBS volume so the volume ID is known
// before the instance launches. The caller must attach the volume after
// RunInstances and clean it up on failure.
func createDataVolume(
	ctx context.Context,
	awsCfg aws.Config,
	providerAws *AwsProvider,
	az string,
) (string, error) {
	cfg := providerAws.Config
	input := &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String(az),
		VolumeType:       types.VolumeType(cfg.DataVolumeType),
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeVolume,
			Tags: []types.Tag{
				{Key: aws.String("Name"), Value: aws.String("devsy-data-" + cfg.MachineID)},
			},
		}},
	}
	if cfg.DataVolumeSnapshotID != "" {
		input.SnapshotId = aws.String(cfg.DataVolumeSnapshotID)
	}
	if cfg.DataVolumeSizeGB > 0 {
		size, err := validatedDiskSize(cfg.DataVolumeSizeGB)
		if err != nil {
			return "", fmt.Errorf("invalid data volume size: %w", err)
		}
		input.Size = &size
	}

	svc := ec2.NewFromConfig(awsCfg)
	vol, err := svc.CreateVolume(ctx, input)
	if err != nil {
		return "", fmt.Errorf("create data volume: %w", err)
	}
	volumeID := aws.ToString(vol.VolumeId)
	log.Debugf("created data volume %s in %s", volumeID, az)

	waiter := ec2.NewVolumeAvailableWaiter(svc)
	if err := waiter.Wait(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	}, 2*time.Minute); err != nil {
		// Best-effort cleanup of the orphaned volume.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		_, _ = svc.DeleteVolume(cleanupCtx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volumeID)})
		return "", fmt.Errorf("wait for data volume %s: %w", volumeID, err)
	}
	return volumeID, nil
}

// attachDataVolume attaches a pre-created volume to an instance and marks it
// for deletion on termination.
func attachDataVolume(
	ctx context.Context,
	providerAws *AwsProvider,
	instanceID, volumeID string,
) error {
	cfg := providerAws.Config
	svc := ec2.NewFromConfig(providerAws.AwsConfig)

	_, err := svc.AttachVolume(ctx, &ec2.AttachVolumeInput{
		Device:     aws.String(cfg.DataVolumeDevice),
		InstanceId: aws.String(instanceID),
		VolumeId:   aws.String(volumeID),
	})
	if err != nil {
		return fmt.Errorf("attach data volume %s to %s: %w", volumeID, instanceID, err)
	}

	// Wait for attachment to complete before modifying block device mapping.
	waiter := ec2.NewVolumeInUseWaiter(svc)
	if err := waiter.Wait(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for volume %s attachment: %w", volumeID, err)
	}
	log.Debugf(
		"attached data volume %s to %s at %s",
		volumeID, instanceID, cfg.DataVolumeDevice,
	)

	// Mark the volume for automatic deletion when the instance terminates.
	_, err = svc.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(instanceID),
		BlockDeviceMappings: []types.InstanceBlockDeviceMappingSpecification{{
			DeviceName: aws.String(cfg.DataVolumeDevice),
			Ebs: &types.EbsInstanceBlockDeviceSpecification{
				DeleteOnTermination: aws.Bool(true),
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("set DeleteOnTermination for volume %s: %w", volumeID, err)
	}
	return nil
}

func deleteVolumeIfPresent(awsCfg aws.Config, volumeID string) {
	if volumeID != "" {
		deleteVolume(awsCfg, volumeID)
	}
}

// deleteVolume is a best-effort cleanup helper. It uses a fresh context so
// cleanup succeeds even when the caller's context is cancelled. If the volume
// is still in-use (e.g. instance is terminating), it waits for it to become
// available before deleting.
func deleteVolume(awsCfg aws.Config, volumeID string) {
	svc := ec2.NewFromConfig(awsCfg)

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelWait()
	waiter := ec2.NewVolumeAvailableWaiter(svc)
	if err := waiter.Wait(waitCtx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	}, 5*time.Minute); err != nil {
		log.Warnf("failed to wait for volume %s to become available: %v", volumeID, err)
	}

	// Use a separate timeout so a slow detach wait cannot leave the delete with
	// an already-expired context and leak the volume.
	deleteCtx, cancelDelete := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancelDelete()
	if _, err := svc.DeleteVolume(deleteCtx, &ec2.DeleteVolumeInput{
		VolumeId: aws.String(volumeID),
	}); err != nil {
		log.Warnf("failed to delete volume %s: %v", volumeID, err)
	}
}
