package aws

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy-provider-aws/pkg/options"
	"github.com/stretchr/testify/assert"
)

func TestK3sInstallScript_DockerModeIsEmpty(t *testing.T) {
	for _, mode := range []string{"", options.DeploymentModeDocker} {
		got := k3sInstallScript(&options.Options{DeploymentMode: mode})
		assert.Empty(t, got, "mode %q should not inject K3s install", mode)
	}
}

func TestK3sInstallScript_KubernetesMode(t *testing.T) {
	got := k3sInstallScript(&options.Options{DeploymentMode: options.DeploymentModeKubernetes})

	assert.Contains(t, got, "get.k3s.io")
	assert.Contains(t, got, "--write-kubeconfig-mode 644")
	assert.Contains(t, got, "wait --for=condition=Ready node")
	assert.NotContains(t, got, "INSTALL_K3S_VERSION")
	assert.NotContains(t, got, "INSTALL_K3S_CHANNEL")
	assert.NotContains(t, got, "--data-dir")
}

func TestK3sInstallScript_DataVolumeUsesDataDir(t *testing.T) {
	withVolume := k3sInstallScript(&options.Options{
		DeploymentMode:      options.DeploymentModeKubernetes,
		DataVolumeSizeGB:    50,
		DataVolumeMountPath: "/data",
	})
	assert.Contains(t, withVolume, "--data-dir /data/k3s")

	withSnapshot := k3sInstallScript(&options.Options{
		DeploymentMode:       options.DeploymentModeKubernetes,
		DataVolumeSnapshotID: "snap-123",
		DataVolumeMountPath:  "/mnt/devsy",
	})
	assert.Contains(t, withSnapshot, "--data-dir /mnt/devsy/k3s")
}

func TestK3sInstallScript_VersionAndChannel(t *testing.T) {
	got := k3sInstallScript(&options.Options{
		DeploymentMode: options.DeploymentModeKubernetes,
		K3sVersion:     "v1.30.2+k3s1",
		K3sChannel:     "stable",
	})

	assert.Contains(t, got, "INSTALL_K3S_VERSION=")
	assert.Contains(t, got, "v1.30.2+k3s1")
	assert.Contains(t, got, "INSTALL_K3S_CHANNEL=")
	assert.Contains(t, got, "stable")
}

func TestK3sInstallScript_QuotesUntrustedValues(t *testing.T) {
	got := k3sInstallScript(&options.Options{
		DeploymentMode: options.DeploymentModeKubernetes,
		K3sVersion:     "v1; rm -rf /",
	})

	// The injected value must be shell-quoted, never interpolated raw.
	assert.NotContains(t, got, "INSTALL_K3S_VERSION=v1; rm -rf /")
	assert.True(t, strings.Contains(got, "'v1; rm -rf /'"), "version should be single-quoted")
}
