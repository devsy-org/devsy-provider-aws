package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/devsy-org/devsy-provider-aws/pkg/options"
	"github.com/devsy-org/devsy/pkg/log"
)

type AwsProvider struct {
	Config           *options.Options
	AwsConfig        aws.Config
	WorkingDirectory string
	accountID        string
}

func NewProvider(ctx context.Context, withFolder bool) (*AwsProvider, error) {
	log.Debugf("creating new AWS provider")
	config, err := options.FromEnv(false, withFolder)
	if err != nil {
		return nil, err
	}

	cfg, err := NewAWSConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	if err := logCallerIdentity(ctx, cfg); err != nil {
		log.Warnf("failed to get caller identity: %v", err)
	}

	if err := configureDefaults(ctx, cfg, config); err != nil {
		return nil, err
	}

	accountID := getCallerAccount(ctx, cfg)

	provider := &AwsProvider{
		Config:    config,
		AwsConfig: cfg,
		accountID: accountID,
	}

	log.Debugf("AWS provider created")
	return provider, nil
}
