package aws

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/smithy-go"
	"github.com/devsy-org/devsy/pkg/log"
)

func awsErrorCode(err error) (string, string) {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return "", ""
	}
	switch apiErr.ErrorCode() {
	case "ExpiredToken", "ExpiredTokenException", "RequestExpired":
		return "aws_credentials_expired", "Refresh AWS credentials and retry."
	case "InstanceLimitExceeded",
		"VcpuLimitExceeded",
		"ResourceLimitExceeded",
		"LimitExceeded",
		"LimitExceededException":
		return "aws_quota_exceeded", "Check AWS service quotas and request an increase."
	case "InvalidSubnetID.NotFound", "InvalidSubnet.Conflict", "InvalidSubnet.Range":
		return "aws_subnet_misconfigured", "Check the configured subnet and region."
	case "TargetNotConnected":
		return "aws_ssm_unreachable", "Check the instance SSM agent, permissions, and network access."
	default:
		return "", ""
	}
}

type awsStep struct {
	operation  string
	machineID  string
	instanceID string
	started    time.Time
}

func logAWSStep(ctx context.Context, step awsStep, err error) {
	if err == nil && !log.DebugEnabled() {
		return
	}
	attrs := []slog.Attr{
		log.AttrComponent("provider"),
		log.AttrProvider("aws"),
		slog.String("aws_step", step.operation),
		log.AttrDurationMs(time.Since(step.started).Milliseconds()),
	}
	if step.machineID != "" {
		attrs = append(attrs, log.AttrMachineID(step.machineID))
	}
	if step.instanceID != "" {
		attrs = append(attrs, slog.String("instance_id", step.instanceID))
	}
	if err != nil {
		if code, remediation := awsErrorCode(err); code != "" {
			attrs = append(attrs, log.AttrErrorCode(code), slog.String("remediation", remediation))
		}
		log.ErrorContext(ctx, "AWS operation failed", attrs...)
		return
	}
	log.DebugContext(ctx, "AWS operation completed", attrs...)
}
