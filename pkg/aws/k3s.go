package aws

import (
	"fmt"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy-provider-aws/pkg/options"
)

const k3sReadyLog = "/var/log/devsy-k3s-install.log"

// k3sInstallScript returns user-data that installs K3s and waits for the node to
// become Ready, or "" outside kubernetes mode. The kubeconfig is world-readable
// (--write-kubeconfig-mode 644) so the unprivileged devsy agent can reach the
// local API server; install failure aborts user-data.
func k3sInstallScript(config *options.Options) string {
	if !config.IsKubernetesMode() {
		return ""
	}

	versionEnv := ""
	if config.K3sVersion != "" {
		versionEnv += fmt.Sprintf(
			"export INSTALL_K3S_VERSION=%s\n",
			shellescape.Quote(config.K3sVersion),
		)
	}
	if config.K3sChannel != "" {
		versionEnv += fmt.Sprintf(
			"export INSTALL_K3S_CHANNEL=%s\n",
			shellescape.Quote(config.K3sChannel),
		)
	}

	// Put K3s storage (its embedded containerd) on the data volume when present.
	// DataVolumeMountPath is validated by mountPathRe, so it is safe to interpolate.
	execArgs := "--write-kubeconfig-mode 644"
	if config.HasDataVolume() {
		execArgs += " --data-dir " + config.DataVolumeMountPath + "/k3s"
	}

	return fmt.Sprintf(`

# --- K3s install (kubernetes deployment mode) ---
{
  %[1]sexport INSTALL_K3S_EXEC=%[3]q
  curl -sfL https://get.k3s.io | sh -
} >> %[2]s 2>&1 || { echo "ERROR: K3s install failed (see %[2]s)" >&2; exit 1; }

# Wait for the K3s API server and the node to become Ready.
K3S_TRIES=0
until k3s kubectl wait --for=condition=Ready node --all --timeout=10s >> %[2]s 2>&1; do
  K3S_TRIES=$((K3S_TRIES + 1))
  if [ "$K3S_TRIES" -ge 30 ]; then
    echo "ERROR: K3s node did not become Ready (see %[2]s)" >&2; exit 1
  fi
  sleep 5
done`,
		versionEnv,  // %[1]s
		k3sReadyLog, // %[2]s
		execArgs,    // %[3]s
	)
}
