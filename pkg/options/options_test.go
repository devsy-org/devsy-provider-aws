package options

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequiredEnv sets the minimum env vars FromEnv needs to reach deployment-mode parsing.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv(AWS_INSTANCE_TYPE, "c5.xlarge")
	t.Setenv(AWS_DISK_SIZE, "40")
}

func TestFromEnv_DeploymentModeDefault(t *testing.T) {
	setRequiredEnv(t)

	opts, err := FromEnv(true, false)
	require.NoError(t, err)
	assert.Equal(t, DeploymentModeDocker, opts.DeploymentMode)
	assert.False(t, opts.IsKubernetesMode())
	assert.Equal(t, "devsy", opts.KubernetesNamespace)
}

func TestFromEnv_SubnetIDsSkipsBlanks(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(AWS_SUBNET_ID, "subnet-1, subnet-2,")

	opts, err := FromEnv(true, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"subnet-1", "subnet-2"}, opts.SubnetIDs)
}

func TestFromEnv_SSHIngressCIDR(t *testing.T) {
	setRequiredEnv(t)
	opts, err := FromEnv(true, false)
	require.NoError(t, err)
	assert.Empty(t, opts.SSHIngressCIDR, "unset by default; caller applies 0.0.0.0/0")

	t.Setenv(AWS_SSH_INGRESS_CIDR, "10.0.0.0/8")
	opts, err = FromEnv(true, false)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0/8", opts.SSHIngressCIDR)
}

func TestFromEnv_DeploymentModeKubernetes(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(AWS_DEPLOYMENT_MODE, DeploymentModeKubernetes)
	t.Setenv(AWS_KUBERNETES_NAMESPACE, "team-a")
	t.Setenv(AWS_K3S_VERSION, "v1.30.2+k3s1")

	opts, err := FromEnv(true, false)
	require.NoError(t, err)
	assert.True(t, opts.IsKubernetesMode())
	assert.Equal(t, "team-a", opts.KubernetesNamespace)
	assert.Equal(t, "v1.30.2+k3s1", opts.K3sVersion)
	assert.Equal(t, "/etc/rancher/k3s/k3s.yaml", opts.K3sKubeconfigPath())
}

func TestFromEnv_DeploymentModeInvalid(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(AWS_DEPLOYMENT_MODE, "podman")

	_, err := FromEnv(true, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), AWS_DEPLOYMENT_MODE)
}
