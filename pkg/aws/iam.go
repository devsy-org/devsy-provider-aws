package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	devsyIAMResourceName       = "devsy-ec2-role"
	iamEC2PolicyName           = "devsy-ec2-policy"
	iamSSMKMSDecryptPolicyName = "ssm-kms-decrypt-policy"
)

func GetDevsyInstanceProfile(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.InstanceProfileArn != "" {
		log.Debugf(
			"using configured instance profile %s",
			provider.Config.InstanceProfileArn,
		)
		return provider.Config.InstanceProfileArn, nil
	}

	svc := iam.NewFromConfig(provider.AwsConfig)

	roleInput := &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(devsyIAMResourceName),
	}

	response, err := svc.GetInstanceProfile(ctx, roleInput)
	if err != nil {
		return CreateDevsyInstanceProfile(ctx, provider)
	}

	log.Debugf("using existing instance profile %s", *response.InstanceProfile.Arn)
	return *response.InstanceProfile.Arn, nil
}

func CreateDevsyInstanceProfile(ctx context.Context, provider *AwsProvider) (string, error) {
	svc := iam.NewFromConfig(provider.AwsConfig)

	if err := createIAMRole(ctx, svc); err != nil {
		return "", err
	}

	if err := attachRolePolicies(ctx, svc, provider.Config.KmsKeyARNForSessionManager); err != nil {
		return "", err
	}

	return createInstanceProfile(ctx, svc)
}

func createIAMRole(ctx context.Context, svc *iam.Client) error {
	assumeRolePolicy := NewEC2AssumeRolePolicy()
	assumeRolePolicyJSON, err := json.Marshal(assumeRolePolicy)
	if err != nil {
		return fmt.Errorf("marshal assume role policy: %w", err)
	}

	_, err = svc.CreateRole(ctx, &iam.CreateRoleInput{
		AssumeRolePolicyDocument: aws.String(string(assumeRolePolicyJSON)),
		RoleName:                 aws.String(devsyIAMResourceName),
	})
	if err != nil {
		var exists *iamtypes.EntityAlreadyExistsException
		if errors.As(err, &exists) {
			return nil
		}
		return fmt.Errorf("create role: %w", err)
	}
	return nil
}

func attachRolePolicies(ctx context.Context, svc *iam.Client, kmsArn string) error {
	ec2Policy := NewDevsyEC2Policy()
	ec2PolicyJSON, err := json.Marshal(ec2Policy)
	if err != nil {
		return fmt.Errorf("marshal EC2 policy: %w", err)
	}

	if _, err = svc.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		PolicyDocument: aws.String(string(ec2PolicyJSON)),
		PolicyName:     aws.String(iamEC2PolicyName),
		RoleName:       aws.String(devsyIAMResourceName),
	}); err != nil {
		return fmt.Errorf("put role policy: %w", err)
	}

	if _, err = svc.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
		RoleName:  aws.String(devsyIAMResourceName),
	}); err != nil {
		return fmt.Errorf("attach SSM policy: %w", err)
	}

	if kmsArn != "" {
		kmsPolicy := NewSSMKMSDecryptPolicy(kmsArn)
		kmsPolicyJSON, err := json.Marshal(kmsPolicy)
		if err != nil {
			return fmt.Errorf("marshal KMS policy: %w", err)
		}

		if _, err = svc.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
			PolicyDocument: aws.String(string(kmsPolicyJSON)),
			PolicyName:     aws.String(iamSSMKMSDecryptPolicyName),
			RoleName:       aws.String(devsyIAMResourceName),
		}); err != nil {
			return fmt.Errorf("put KMS decrypt policy: %w", err)
		}
	}

	return nil
}

func createInstanceProfile(ctx context.Context, svc *iam.Client) (string, error) {
	arn, err := createOrGetInstanceProfile(ctx, svc)
	if err != nil {
		return "", err
	}

	if err := attachRoleToProfile(ctx, svc); err != nil {
		return "", err
	}

	if err := waitForInstanceProfile(ctx, svc); err != nil {
		return "", err
	}

	return arn, nil
}

func createOrGetInstanceProfile(ctx context.Context, svc *iam.Client) (string, error) {
	response, err := svc.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(devsyIAMResourceName),
	})
	if err != nil {
		var exists *iamtypes.EntityAlreadyExistsException
		if errors.As(err, &exists) {
			getResponse, err := svc.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
				InstanceProfileName: aws.String(devsyIAMResourceName),
			})
			if err != nil {
				return "", fmt.Errorf("get instance profile: %w", err)
			}
			return *getResponse.InstanceProfile.Arn, nil
		}
		return "", fmt.Errorf("create instance profile: %w", err)
	}
	return *response.InstanceProfile.Arn, nil
}

func attachRoleToProfile(ctx context.Context, svc *iam.Client) error {
	_, err := svc.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(devsyIAMResourceName),
		RoleName:            aws.String(devsyIAMResourceName),
	})
	if err != nil {
		var already *iamtypes.EntityAlreadyExistsException
		if !errors.As(err, &already) {
			return fmt.Errorf("add role to instance profile: %w", err)
		}
	}
	return nil
}

func waitForInstanceProfile(ctx context.Context, svc *iam.Client) error {
	waiter := iam.NewInstanceProfileExistsWaiter(svc)
	if err := waiter.Wait(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(devsyIAMResourceName),
	}, 2*time.Minute); err != nil {
		return fmt.Errorf("wait for instance profile: %w", err)
	}
	return nil
}

func applyInstanceProfile(
	ctx context.Context,
	providerAws *AwsProvider,
	instance *ec2.RunInstancesInput,
) error {
	log.Debugf("getting instance profile")
	profile, err := GetDevsyInstanceProfile(ctx, providerAws)
	if err != nil {
		return fmt.Errorf("get instance profile: %w", err)
	}
	log.Debugf("using instance profile: %s", profile)
	instance.IamInstanceProfile = &types.IamInstanceProfileSpecification{
		Arn: aws.String(profile),
	}
	return nil
}
