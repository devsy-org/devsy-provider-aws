package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/devsy-org/devsy/pkg/log"
)

func TestAWSErrorCode(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"ExpiredToken", "aws_credentials_expired"},
		{"InstanceLimitExceeded", "aws_quota_exceeded"},
		{"InvalidSubnetID.NotFound", "aws_subnet_misconfigured"},
		{"TargetNotConnected", "aws_ssm_unreachable"},
		{"Unknown", ""},
	} {
		err := fmt.Errorf("wrapped: %w", &smithy.GenericAPIError{Code: tc.input})
		code, remediation := awsErrorCode(err)
		if code != tc.want || (code != "" && remediation == "") {
			t.Errorf("%s: code %q remediation %q", tc.input, code, remediation)
		}
	}
	if code, _ := awsErrorCode(errors.New("ExpiredToken")); code != "" {
		t.Fatalf("classified untyped error as %q", code)
	}
}

func TestAWSStepDiagnostic(t *testing.T) {
	logs := log.InitTestObserved(t, slog.LevelDebug)
	err := &smithy.GenericAPIError{Code: "ExpiredToken", Message: "secret-value-canary"}
	logAWSStep(
		context.Background(),
		awsStep{
			operation:  "ec2.run_instances",
			machineID:  "machine-1",
			instanceID: "i-123",
			started:    time.Now(),
		},
		err,
	)
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	attrs := entries[0].AttrsMap()
	for key, want := range map[string]string{
		"component":   "provider",
		"provider":    "aws",
		"machine_id":  "machine-1",
		"instance_id": "i-123",
		"aws_step":    "ec2.run_instances",
		"error_code":  "aws_credentials_expired",
	} {
		if attrs[key] != want {
			t.Errorf("%s = %v, want %s", key, attrs[key], want)
		}
	}
	if entries[0].Message != "AWS operation failed" || attrs["duration_ms"] == nil {
		t.Fatalf("unexpected diagnostic: %#v", entries[0])
	}
}
