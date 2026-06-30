package aws

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy/pkg/log"
)

func GetDefaultAMI(ctx context.Context, cfg aws.Config, instanceType string) (string, error) {
	svc := ec2.NewFromConfig(cfg)

	architecture := resolveArchitecture(ctx, svc, instanceType)

	// Try Ubuntu 24.04 LTS (Noble) first, then fall back to 22.04 LTS (Jammy)
	patterns := []string{
		fmt.Sprintf("ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-%s-server-*", architecture),
		fmt.Sprintf("ubuntu/images/hvm-ssd/ubuntu-noble-24.04-%s-server-*", architecture),
		fmt.Sprintf("ubuntu/images/hvm-ssd-gp3/ubuntu-jammy-22.04-%s-server-*", architecture),
		fmt.Sprintf("ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-%s-server-*", architecture),
	}

	for _, pattern := range patterns {
		input := &ec2.DescribeImagesInput{
			Owners: []string{"099720109477"}, // Canonical
			Filters: []types.Filter{
				{
					Name:   aws.String("name"),
					Values: []string{pattern},
				},
				{
					Name:   aws.String("state"),
					Values: []string{"available"},
				},
				{
					Name:   aws.String("root-device-type"),
					Values: []string{"ebs"},
				},
			},
		}

		result, err := svc.DescribeImages(ctx, input)
		if err != nil {
			return "", err
		}

		if len(result.Images) == 0 {
			continue
		}

		// Sort by creation date to get the latest
		sort.Slice(result.Images, func(i, j int) bool {
			iTime, _ := time.Parse("2006-01-02T15:04:05.000Z", *result.Images[i].CreationDate)
			jTime, _ := time.Parse("2006-01-02T15:04:05.000Z", *result.Images[j].CreationDate)
			return iTime.After(jTime)
		})

		return *result.Images[0].ImageId, nil
	}

	return "", fmt.Errorf("no matching Ubuntu LTS AMI found for architecture %s", architecture)
}

// resolveArchitecture maps an instance type to the Ubuntu AMI architecture token
// ("amd64" or "arm64"). It queries instance-type metadata so Arm families that
// the name alone does not reveal (a1, c7gn, m6gd, x2gd, im4gn, ...) are detected
// correctly, falling back to a name heuristic if the API is unavailable.
func resolveArchitecture(ctx context.Context, svc *ec2.Client, instanceType string) string {
	out, err := svc.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err == nil && len(out.InstanceTypes) > 0 && out.InstanceTypes[0].ProcessorInfo != nil {
		if slices.Contains(
			out.InstanceTypes[0].ProcessorInfo.SupportedArchitectures,
			types.ArchitectureTypeArm64,
		) {
			return "arm64"
		}
		return "amd64"
	}

	log.Debugf("falling back to name-based architecture detection for %s: %v", instanceType, err)
	if isArmFamily(strings.Split(instanceType, ".")[0]) {
		return "arm64"
	}
	return "amd64"
}

// isArmFamily reports whether an instance-type family (e.g. "c7gn") is AWS
// Graviton/Arm. Arm families carry a "g" in the capability suffix that follows
// the generation digit, e.g. m6g, c7gn, m6gd, x2gd, im4gn, is4gen; a1 is the
// one Arm family without that marker.
func isArmFamily(family string) bool {
	if family == "a1" {
		return true
	}
	i := strings.IndexFunc(family, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return false
	}
	suffix := family[i:] // e.g. "7gn", "6gd", "4gn"
	return strings.ContainsRune(suffix, 'g')
}

func GetAMIRootDevice(ctx context.Context, cfg aws.Config, diskImage string) (string, error) {
	svc := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeImagesInput{
		ImageIds: []string{
			diskImage,
		},
	}
	result, err := svc.DescribeImages(ctx, input)
	if err != nil {
		return "", err
	}

	// Struct spec: https://docs.aws.amazon.com/sdk-for-go/api/service/ec2/#Image
	if len(result.Images) == 0 || result.Images[0].RootDeviceName == nil ||
		*result.Images[0].RootDeviceName == "" {
		return defaultRootDevice, nil
	}

	return *result.Images[0].RootDeviceName, nil
}
