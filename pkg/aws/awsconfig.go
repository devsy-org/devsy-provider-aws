package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/devsy-org/devsy-provider-aws/pkg/options"
	"github.com/devsy-org/devsy/pkg/log"
)

// detect if we're in an ec2 instance.
func isEC2Instance(ctx context.Context) bool {
	httpClient := &http.Client{Timeout: 1 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://instance-data.ec2.internal", nil)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return true
}

func configureDefaults(
	ctx context.Context,
	cfg aws.Config,
	config *options.Options,
) error {
	isEC2 := isEC2Instance(ctx)
	log.Debugf("running in EC2 instance: %v", isEC2)

	if config.DiskImage == "" && !isEC2 {
		if err := setDefaultAMI(ctx, cfg, config); err != nil {
			return err
		}
	}

	if config.RootDevice == "" && !isEC2 && config.DiskImage != "" {
		setRootDevice(ctx, cfg, config)
	}

	if config.RootDevice == "" {
		config.RootDevice = defaultRootDevice
	}

	return nil
}

func setDefaultAMI(
	ctx context.Context,
	cfg aws.Config,
	config *options.Options,
) error {
	log.Debugf(
		"disk image not specified, fetching default AMI for instance type %s",
		config.MachineType,
	)
	image, err := GetDefaultAMI(ctx, cfg, config.MachineType)
	if err != nil {
		return err
	}
	log.Debugf("using default AMI %s", image)
	config.DiskImage = image
	return nil
}

func setRootDevice(ctx context.Context, cfg aws.Config, config *options.Options) {
	log.Debugf("determining root device for AMI %s", config.DiskImage)
	device, err := GetAMIRootDevice(ctx, cfg, config.DiskImage)
	if err != nil {
		log.Debugf(
			"could not determine root device for AMI %s: %v, using default %s",
			config.DiskImage,
			err,
			defaultRootDevice,
		)
		config.RootDevice = defaultRootDevice
	} else {
		log.Debugf("using root device: %s", device)
		config.RootDevice = device
	}
}

func NewAWSConfig(
	ctx context.Context,
	options *options.Options,
) (aws.Config, error) {
	log.Debugf("configuring AWS SDK for region %s", options.Zone)
	opts, err := buildConfigOptions(ctx, options)
	if err != nil {
		return aws.Config{}, err
	}
	cfg, err := awsConfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	log.Debugf("AWS SDK configured")
	return cfg, nil
}

func buildConfigOptions(
	ctx context.Context,
	options *options.Options,
) ([]func(*awsConfig.LoadOptions) error, error) {
	var opts []func(*awsConfig.LoadOptions) error

	if options.Zone != "" {
		opts = append(opts, awsConfig.WithRegion(options.Zone))
	}

	switch {
	case options.AccessKeyID != "" && options.SecretAccessKey != "":
		log.Debugf("using provided AWS credentials")
		opts = append(opts, awsConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     options.AccessKeyID,
				SecretAccessKey: options.SecretAccessKey,
				SessionToken:    options.SessionToken,
			},
		}))
		opts = append(opts, awsConfig.WithSharedConfigFiles([]string{}))
		opts = append(opts, awsConfig.WithSharedCredentialsFiles([]string{}))
	case options.CustomCredentialCommand != "":
		creds, err := executeCredentialCommand(ctx, options.CustomCredentialCommand)
		if err != nil {
			return nil, fmt.Errorf("custom credential command: %w", err)
		}
		opts = append(
			opts,
			awsConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{Value: creds}),
		)
		opts = append(opts, awsConfig.WithSharedConfigFiles([]string{}))
		opts = append(opts, awsConfig.WithSharedCredentialsFiles([]string{}))
	default:
		profile := os.Getenv("AWS_PROFILE")
		if profile != "" {
			log.Debugf("using AWS profile %s", profile)
		} else {
			log.Debugf("using default AWS credential chain")
		}
	}

	return opts, nil
}

func executeCredentialCommand(
	ctx context.Context,
	command string,
) (aws.Credentials, error) {
	log.Debugf("using custom credential command: %s", command)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var output bytes.Buffer
	// command is the operator-supplied CUSTOM_AWS_CREDENTIAL_COMMAND helper.
	//nolint:gosec // operator-provided credential command
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout = &output
	cmd.Stderr = log.Writer(log.LevelError)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return aws.Credentials{}, fmt.Errorf("credential command timed out after 30s")
		}
		return aws.Credentials{}, fmt.Errorf("run command %q: %w", command, err)
	}

	var creds aws.Credentials
	if err := json.Unmarshal(output.Bytes(), &creds); err != nil {
		return aws.Credentials{}, fmt.Errorf("parse AWS credential JSON output: %w", err)
	}

	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return aws.Credentials{}, fmt.Errorf(
			"custom credential command output missing required fields",
		)
	}

	return creds, nil
}

func logCallerIdentity(ctx context.Context, cfg aws.Config) error {
	svc := sts.NewFromConfig(cfg)
	result, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}

	log.Debugf("AWS provider initialized - account: %s, region: %s, arn: %s",
		aws.ToString(result.Account),
		cfg.Region,
		aws.ToString(result.Arn))
	return nil
}

// getCallerAccount returns the AWS account ID for logging context.
func getCallerAccount(ctx context.Context, cfg aws.Config) string {
	svc := sts.NewFromConfig(cfg)
	result, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "unknown"
	}
	return aws.ToString(result.Account)
}
