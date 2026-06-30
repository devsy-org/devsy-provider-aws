package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy/pkg/log"
)

func GetDevsySecurityGroups(
	ctx context.Context,
	provider *AwsProvider,
	vpcID string,
) ([]string, error) {
	if provider.Config.SecurityGroupID != "" {
		sgs := strings.Split(provider.Config.SecurityGroupID, ",")
		log.Debugf("using configured security groups %v", sgs)
		return sgs, nil
	}

	svc := ec2.NewFromConfig(provider.AwsConfig)
	input := &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devsy"),
				Values: []string{
					tagKeyDevsy,
				},
			},
		},
	}

	if vpcID != "" {
		input.Filters = append(input.Filters, types.Filter{
			Name:   aws.String("vpc-id"),
			Values: []string{vpcID},
		})
	}

	result, err := svc.DescribeSecurityGroups(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describe security groups: %w", err)
	}

	if len(result.SecurityGroups) == 0 {
		sg, err := CreateDevsySecurityGroup(ctx, provider, vpcID)
		if err != nil {
			return nil, err
		}

		log.Debugf("created new security group %s", sg)
		return []string{sg}, nil
	}

	sgs := []string{}
	for res := range result.SecurityGroups {
		sgs = append(sgs, *result.SecurityGroups[res].GroupId)
	}

	log.Debugf("using existing security groups %v", sgs)
	return sgs, nil
}

func CreateDevsySecurityGroup(
	ctx context.Context,
	provider *AwsProvider,
	vpcID string,
) (string, error) {
	svc := ec2.NewFromConfig(provider.AwsConfig)

	if vpcID == "" {
		var err error
		vpcID, err = GetDevsyVPC(ctx, provider)
		if err != nil {
			return "", err
		}
	}

	// Create the security group with the VPC, name, and description.
	result, err := svc.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("devsy"),
		Description: aws.String("Default Security Group for Devsy"),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "security-group",
				Tags: []types.Tag{
					{
						Key:   aws.String(tagKeyDevsy),
						Value: aws.String(tagKeyDevsy),
					},
				},
			},
		},
		VpcId: aws.String(vpcID),
	})
	if err != nil {
		return "", err
	}

	groupID := *result.GroupId

	// No need to open ssh port if use session manager.
	if provider.Config.UseSessionManager {
		return groupID, nil
	}

	if err := authorizeSSHIngress(ctx, svc, groupID); err != nil {
		if _, delErr := svc.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
			GroupId: aws.String(groupID),
		}); delErr != nil {
			log.Warnf("failed to clean up security group %s: %v", groupID, delErr)
		}
		return "", err
	}

	return groupID, nil
}

func authorizeSSHIngress(ctx context.Context, svc *ec2.Client, groupID string) error {
	_, err := svc.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(22),
				ToPort:     aws.Int32(22),
				IpRanges: []types.IpRange{
					{
						CidrIp: aws.String("0.0.0.0/0"),
					},
				},
			},
		},
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "security-group-rule",
				Tags: []types.Tag{
					{
						Key:   aws.String(tagKeyDevsy),
						Value: aws.String("devsy-ingress"),
					},
				},
			},
		},
	})
	return err
}
