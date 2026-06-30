package aws

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func GetDefaultAMI(ctx context.Context, cfg aws.Config, instanceType string) (string, error) {
	svc := ec2.NewFromConfig(cfg)

	architecture := "amd64"
	if strings.HasSuffix(strings.Split(instanceType, ".")[0], "g") {
		architecture = "arm64"
	}

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
	if len(result.Images) == 0 || *result.Images[0].RootDeviceName == "" {
		return defaultRootDevice, nil
	}

	return *result.Images[0].RootDeviceName, nil
}
