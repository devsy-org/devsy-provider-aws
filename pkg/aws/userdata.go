package aws

import (
	"encoding/base64"
	"fmt"

	"github.com/devsy-org/devsy-provider-aws/pkg/options"
	"github.com/devsy-org/devsy/pkg/ssh"
)

func GetInjectKeypairScript(config *options.Options) (string, error) {
	publicKeyBase, err := ssh.GetPublicKeyBase(config.MachineFolder)
	if err != nil {
		return "", err
	}

	// Validate it is well-formed base64, but inject the encoded form and decode
	// on the instance so a key comment containing shell metacharacters (", `,
	// $()) can never be interpreted by the shell.
	if _, err := base64.StdEncoding.DecodeString(publicKeyBase); err != nil {
		return "", err
	}

	resultScript := `#!/bin/sh
useradd devsy -d /home/devsy
mkdir -p /home/devsy
if grep -q sudo /etc/group; then
	usermod -aG sudo devsy
elif grep -q wheel /etc/group; then
	usermod -aG wheel devsy
fi
echo "devsy ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/91-devsy
mkdir -p /home/devsy/.ssh
echo "` + publicKeyBase + `" | base64 -d >> /home/devsy/.ssh/authorized_keys
chmod 0700 /home/devsy/.ssh
chmod 0600 /home/devsy/.ssh/authorized_keys
chown -R devsy:devsy /home/devsy`

	resultScript += hookSnippet("post-ssh", config.HookPostSSH)
	resultScript += dataVolumeMountScript(config)
	resultScript += k3sInstallScript(config)
	resultScript += hookSnippet("post-volume", config.HookPostVolume)

	return base64.StdEncoding.EncodeToString([]byte(resultScript)), nil
}

// dataVolumeMountScript returns a shell snippet that resolves the data volume
// device (including NVMe translation on Nitro instances), formats it if needed,
// and adds a persistent fstab entry. NVMe resolution uses ebsnvme-id when
// available (Amazon Linux) and falls back to matching the volume serial number
// exposed in sysfs — no nvme-cli required.
// See: https://docs.aws.amazon.com/ebs/latest/userguide/nvme-ebs-volumes.html
func dataVolumeMountScript(config *options.Options) string {
	if !config.HasDataVolume() {
		return ""
	}

	return fmt.Sprintf(`

# Mount secondary data volume. On Nitro instances, resolve NVMe device names.
# See: https://docs.aws.amazon.com/ebs/latest/userguide/nvme-ebs-volumes.html
DATA_DEV="%[1]s"
SNAPSHOT_ID="%[3]s"
VOLUME_ID="%[4]s"
`+dataVolumeResolveSnippet()+dataVolumeFormatMountSnippet(),
		config.DataVolumeDevice,     // %[1]s
		config.DataVolumeMountPath,  // %[2]s
		config.DataVolumeSnapshotID, // %[3]s
		config.DataVolumeID,         // %[4]s
	)
}

// dataVolumeResolveSnippet returns a shell snippet that waits for the data
// volume to appear and resolves NVMe device names in a single retry loop.
// On each 2-second iteration it checks: direct block device, ebsnvme-id
// mapping, then sysfs serial scan. Breaks as soon as DATA_DEV is resolved.
func dataVolumeResolveSnippet() string {
	return `
EXPECTED_SHORT=$(echo "$DATA_DEV" | sed 's|^/dev/||')
VOL_SERIAL=$(echo "$VOLUME_ID" | tr -d '-')
TRIES=0
while [ "$TRIES" -lt 30 ]; do
  if [ -b "$DATA_DEV" ]; then break; fi
  if command -v ebsnvme-id >/dev/null 2>&1; then
    for nvmedev in /dev/nvme[0-9]*n1; do
      [ -b "$nvmedev" ] || continue
      MAPPED=$(ebsnvme-id -b "$nvmedev" 2>/dev/null | sed 's|^/dev/||')
      if [ "$MAPPED" = "$EXPECTED_SHORT" ]; then DATA_DEV="$nvmedev"; break 2; fi
    done
  fi
  if [ -n "$VOL_SERIAL" ]; then
    for nvmedev in /dev/nvme[0-9]*n1; do
      [ -b "$nvmedev" ] || continue
      SERIAL=$(cat "/sys/block/$(basename "$nvmedev")/device/serial" 2>/dev/null | tr -d ' ')
      if [ "$SERIAL" = "$VOL_SERIAL" ]; then DATA_DEV="$nvmedev"; break 2; fi
    done
  fi
  sleep 2; TRIES=$((TRIES + 1))
done
if [ ! -b "$DATA_DEV" ]; then
  echo "ERROR: data volume device %[1]s (volume $VOLUME_ID) not found" >&2; exit 1
fi
`
}

// dataVolumeFormatMountSnippet returns the format, fstab, and mount logic.
func dataVolumeFormatMountSnippet() string {
	return `mkdir -p "%[2]s"
if ! blkid "$DATA_DEV" >/dev/null 2>&1; then
  if [ -n "$SNAPSHOT_ID" ]; then
    echo "ERROR: snapshot volume $DATA_DEV has no recognizable filesystem" >&2; exit 1
  fi
  mkfs.ext4 -q "$DATA_DEV"
fi
DATA_FSTYPE=$(blkid -s TYPE -o value "$DATA_DEV")
if [ -z "$DATA_FSTYPE" ]; then
  echo "ERROR: failed to detect filesystem type for $DATA_DEV" >&2; exit 1
fi
DATA_UUID=$(blkid -s UUID -o value "$DATA_DEV")
if [ -z "$DATA_UUID" ]; then
  echo "ERROR: failed to get UUID for data volume $DATA_DEV" >&2; exit 1
fi
if ! grep -q "UUID=$DATA_UUID" /etc/fstab; then
  echo "UUID=$DATA_UUID %[2]s $DATA_FSTYPE defaults,nofail 0 2" >> /etc/fstab
fi
mount -a
if ! mountpoint -q "%[2]s"; then
  echo "ERROR: failed to mount data volume at %[2]s" >&2; exit 1
fi
case "$DATA_FSTYPE" in ext4) resize2fs "$DATA_DEV" 2>/dev/null;; xfs) xfs_growfs "%[2]s" 2>/dev/null;; esac
chown devsy:devsy "%[2]s"
mkdir -p "%[2]s/.containerd-root" || { echo "ERROR: failed to create containerd root dir" >&2; exit 1; }
mkdir -p /var/lib/containerd || { echo "ERROR: failed to create /var/lib/containerd" >&2; exit 1; }
if ! mountpoint -q /var/lib/containerd; then
  if ! mount --bind "%[2]s/.containerd-root" /var/lib/containerd; then
    echo "ERROR: failed to bind-mount containerd root" >&2; exit 1
  fi
fi
if ! grep -qF '%[2]s/.containerd-root /var/lib/containerd' /etc/fstab; then
  FSTAB_ENTRY="%[2]s/.containerd-root /var/lib/containerd none bind 0 0"
  echo "$FSTAB_ENTRY" >> /etc/fstab || { echo "ERROR: failed to update /etc/fstab" >&2; exit 1; }
fi
if [ -n "$SNAPSHOT_ID" ] && [ -d "%[2]s/containers" ]; then
  rm -rf "%[2]s/containers"/* || true
  rm -rf "%[2]s/network/files"/* || true
fi`
}
