package aws

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/devsy-org/devsy-provider-aws/pkg/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCustomTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []types.Tag
	}{
		{name: "empty", in: "", want: nil},
		{name: "no match", in: "garbage", want: nil},
		{
			name: "single",
			in:   "Name=Env,Value=prod",
			want: []types.Tag{{Key: aws.String("Env"), Value: aws.String("prod")}},
		},
		{
			name: "multiple",
			in:   "Name=Env,Value=prod Name=Team,Value=core",
			want: []types.Tag{
				{Key: aws.String("Env"), Value: aws.String("prod")},
				{Key: aws.String("Team"), Value: aws.String("core")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCustomTags(tt.in)
			require.Len(t, got, len(tt.want))
			for i := range tt.want {
				assert.Equal(t, *tt.want[i].Key, *got[i].Key)
				assert.Equal(t, *tt.want[i].Value, *got[i].Value)
			}
		})
	}
}

func subnet(id, az string, ips int32) types.Subnet {
	return types.Subnet{
		SubnetId:                aws.String(id),
		VpcId:                   aws.String("vpc-1"),
		AvailabilityZone:        aws.String(az),
		AvailableIpAddressCount: aws.Int32(ips),
		MapPublicIpOnLaunch:     aws.Bool(true),
	}
}

func TestSelectSubnetWithMostIPs(t *testing.T) {
	subnets := []types.Subnet{
		subnet("s-a", "us-east-1a", 10),
		subnet("s-b", "us-east-1b", 50),
		subnet("s-c", "us-east-1a", 30),
	}

	got := selectSubnetWithMostIPs(subnets, "")
	require.NotNil(t, got)
	assert.Equal(t, "s-b", *got.SubnetId)

	gotAZ := selectSubnetWithMostIPs(subnets, "us-east-1a")
	require.NotNil(t, gotAZ)
	assert.Equal(t, "s-c", *gotAZ.SubnetId)

	assert.Nil(t, selectSubnetWithMostIPs(subnets, "eu-west-1a"))
}

func TestIsPublicSubnetInVPC(t *testing.T) {
	pub := subnet("s-a", "z", 5)
	priv := subnet("s-b", "z", 5)
	priv.MapPublicIpOnLaunch = aws.Bool(false)
	assert.True(t, isPublicSubnetInVPC(&pub, "vpc-1"))
	assert.False(t, isPublicSubnetInVPC(&pub, "vpc-2"))
	assert.False(t, isPublicSubnetInVPC(&priv, "vpc-1"))
}

func TestIsDevsyTagged(t *testing.T) {
	assert.True(t, isDevsyTagged([]types.Tag{
		{Key: aws.String(tagKeyDevsy), Value: aws.String(tagKeyDevsy)},
	}))
	assert.False(t, isDevsyTagged([]types.Tag{
		{Key: aws.String(tagKeyDevsy), Value: aws.String("other")},
	}))
	assert.False(t, isDevsyTagged(nil))
}

func TestValidatedDiskSize(t *testing.T) {
	got, err := validatedDiskSize(40)
	require.NoError(t, err)
	assert.Equal(t, int32(40), got)

	_, err = validatedDiskSize(-1)
	assert.Error(t, err)
}

func TestMachineHost(t *testing.T) {
	assert.Equal(t, "host.example", Machine{Hostname: "host.example", PublicIP: "1.2.3.4"}.Host())
	assert.Equal(t, "1.2.3.4", Machine{PublicIP: "1.2.3.4", PrivateIP: "10.0.0.1"}.Host())
	assert.Equal(t, "10.0.0.1", Machine{PrivateIP: "10.0.0.1"}.Host())
}

func TestGetInjectKeypairScript(t *testing.T) {
	dir := t.TempDir()

	dockerScript := decodeUserData(t, &options.Options{
		MachineFolder:  dir,
		DeploymentMode: options.DeploymentModeDocker,
	})
	assert.Contains(t, dockerScript, "useradd devsy")
	assert.Contains(t, dockerScript, "/home/devsy/.ssh/authorized_keys")
	assert.NotContains(t, dockerScript, "get.k3s.io")

	k8sScript := decodeUserData(t, &options.Options{
		MachineFolder:  dir,
		DeploymentMode: options.DeploymentModeKubernetes,
	})
	assert.Contains(t, k8sScript, "useradd devsy")
	assert.Contains(t, k8sScript, "get.k3s.io")

	// With a data volume, the volume must mount before K3s installs, and K3s
	// must be pointed at a data-dir on that volume.
	k8sWithVolume := decodeUserData(t, &options.Options{
		MachineFolder:       dir,
		DeploymentMode:      options.DeploymentModeKubernetes,
		DataVolumeSizeGB:    50,
		DataVolumeDevice:    "/dev/xvdf",
		DataVolumeMountPath: "/data",
		DataVolumeType:      "gp3",
	})
	mountIdx := strings.Index(k8sWithVolume, "Mount secondary data volume")
	k3sIdx := strings.Index(k8sWithVolume, "get.k3s.io")
	require.Positive(t, mountIdx)
	require.Positive(t, k3sIdx)
	assert.Less(t, mountIdx, k3sIdx, "data volume must mount before K3s installs")
	assert.Contains(t, k8sWithVolume, "--data-dir /data/k3s")
}

func decodeUserData(t *testing.T, cfg *options.Options) string {
	t.Helper()
	encoded, err := GetInjectKeypairScript(cfg)
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	return string(raw)
}

func TestNewMachineFromInstanceHostnameTag(t *testing.T) {
	inst := types.Instance{
		InstanceId:       aws.String("i-123"),
		PrivateIpAddress: aws.String("10.0.0.1"),
		PublicIpAddress:  aws.String("1.2.3.4"),
		State:            &types.InstanceState{Name: types.InstanceStateNameRunning},
		Tags: []types.Tag{
			{Key: aws.String(tagKeyHostname), Value: aws.String("box.devsy.dev")},
		},
	}
	m := NewMachineFromInstance(inst)
	assert.Equal(t, "i-123", m.InstanceID)
	assert.Equal(t, "box.devsy.dev", m.Hostname)
	assert.Equal(t, "running", m.Status)
	assert.Equal(t, "box.devsy.dev", m.Host())
}
