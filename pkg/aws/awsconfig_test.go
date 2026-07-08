package aws

import (
	"context"
	"os"
	"testing"

	"github.com/devsy-org/devsy-provider-aws/pkg/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProfile   = "some-profile"
	credCommand   = `printf '{"AccessKeyID":"AKIA","SecretAccessKey":"secret"}'`
	staticOptsLen = 3
)

func TestBuildConfigOptionsClearsProfile(t *testing.T) {
	tests := []struct {
		name string
		opts *options.Options
	}{
		{
			name: "static credentials",
			opts: &options.Options{AccessKeyID: "AKIA", SecretAccessKey: "secret"},
		},
		{
			name: "custom credential command",
			opts: &options.Options{CustomCredentialCommand: credCommand},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_PROFILE", testProfile)
			t.Setenv("AWS_DEFAULT_PROFILE", "some-default-profile")

			_, err := buildConfigOptions(context.Background(), tt.opts)
			require.NoError(t, err)

			_, hasProfile := os.LookupEnv("AWS_PROFILE")
			assert.False(t, hasProfile, "AWS_PROFILE should be cleared")
			_, hasDefault := os.LookupEnv("AWS_DEFAULT_PROFILE")
			assert.False(t, hasDefault, "AWS_DEFAULT_PROFILE should be cleared")
		})
	}
}

func TestBuildConfigOptionsCredentialPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		opts        *options.Options
		wantOptsLen int
		wantProfile string
	}{
		{
			name:        "static keys win over profile",
			profile:     testProfile,
			opts:        &options.Options{AccessKeyID: "AKIA", SecretAccessKey: "secret"},
			wantOptsLen: staticOptsLen,
		},
		{
			name:        "custom command wins over profile",
			profile:     testProfile,
			opts:        &options.Options{CustomCredentialCommand: credCommand},
			wantOptsLen: staticOptsLen,
		},
		{
			name:        "explicit profile is honored and adds no options",
			profile:     testProfile,
			opts:        &options.Options{},
			wantProfile: testProfile,
		},
		{
			name: "default chain adds no options",
			opts: &options.Options{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_PROFILE", tt.profile)

			opts, err := buildConfigOptions(context.Background(), tt.opts)
			require.NoError(t, err)
			assert.Len(t, opts, tt.wantOptsLen)
			assert.Equal(t, tt.wantProfile, os.Getenv("AWS_PROFILE"))
		})
	}
}
