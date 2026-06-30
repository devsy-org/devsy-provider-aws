package options

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	devicePathRe = regexp.MustCompile(`^/dev/[a-zA-Z0-9/]+$`)
	mountPathRe  = regexp.MustCompile(`^/[a-zA-Z0-9/_.-]+$`)
)

var (
	AWS_AMI                             = "AWS_AMI"
	AWS_DISK_SIZE                       = "AWS_DISK_SIZE"
	AWS_ROOT_DEVICE                     = "AWS_ROOT_DEVICE"
	AWS_INSTANCE_TYPE                   = "AWS_INSTANCE_TYPE"
	AWS_REGION                          = "AWS_REGION"
	AWS_SECURITY_GROUP_ID               = "AWS_SECURITY_GROUP_ID"
	AWS_SUBNET_ID                       = "AWS_SUBNET_ID"
	AWS_VPC_ID                          = "AWS_VPC_ID"
	AWS_AVAILABILITY_ZONE               = "AWS_AVAILABILITY_ZONE"
	AWS_INSTANCE_TAGS                   = "AWS_INSTANCE_TAGS"
	AWS_INSTANCE_PROFILE_ARN            = "AWS_INSTANCE_PROFILE_ARN"
	AWS_USE_NESTED_VIRTUALIZATION       = "AWS_USE_NESTED_VIRTUALIZATION"
	AWS_USE_INSTANCE_CONNECT_ENDPOINT   = "AWS_USE_INSTANCE_CONNECT_ENDPOINT"
	AWS_INSTANCE_CONNECT_ENDPOINT_ID    = "AWS_INSTANCE_CONNECT_ENDPOINT_ID"
	AWS_USE_SPOT_INSTANCE               = "AWS_USE_SPOT_INSTANCE"
	AWS_SPOT_INSTANCE_TYPE              = "AWS_SPOT_INSTANCE_TYPE"
	AWS_USE_SESSION_MANAGER             = "AWS_USE_SESSION_MANAGER"
	AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER = "AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER"
	AWS_USE_ROUTE53                     = "AWS_USE_ROUTE53"
	AWS_ROUTE53_ZONE_NAME               = "AWS_ROUTE53_ZONE_NAME"
	AWS_ACCESS_KEY_ID                   = "AWS_ACCESS_KEY_ID"
	AWS_SECRET_ACCESS_KEY               = "AWS_SECRET_ACCESS_KEY"         //nolint:gosec // option name, not a secret
	AWS_SESSION_TOKEN                   = "AWS_SESSION_TOKEN"             //nolint:gosec // option name, not a secret
	CUSTOM_AWS_CREDENTIAL_COMMAND       = "CUSTOM_AWS_CREDENTIAL_COMMAND" //nolint:gosec // option name, not a secret

	// User-data hook options (optional).
	AWS_HOOK_POST_SSH    = "AWS_HOOK_POST_SSH"
	AWS_HOOK_POST_VOLUME = "AWS_HOOK_POST_VOLUME"

	// Data volume options (all optional).
	AWS_DATA_VOLUME_SNAPSHOT_ID = "AWS_DATA_VOLUME_SNAPSHOT_ID"
	AWS_DATA_VOLUME_SIZE        = "AWS_DATA_VOLUME_SIZE"
	AWS_DATA_VOLUME_DEVICE      = "AWS_DATA_VOLUME_DEVICE"
	AWS_DATA_VOLUME_MOUNT_PATH  = "AWS_DATA_VOLUME_MOUNT_PATH"
	AWS_DATA_VOLUME_TYPE        = "AWS_DATA_VOLUME_TYPE"

	// Deployment mode options.
	AWS_DEPLOYMENT_MODE = "AWS_DEPLOYMENT_MODE"

	// Kubernetes (K3s) mode options (only used when deployment mode is kubernetes).
	AWS_K3S_VERSION          = "AWS_K3S_VERSION"
	AWS_K3S_CHANNEL          = "AWS_K3S_CHANNEL"
	AWS_KUBERNETES_NAMESPACE = "AWS_KUBERNETES_NAMESPACE"
)

// Deployment modes select how the devcontainer is run on the instance.
const (
	DeploymentModeDocker     = "docker"
	DeploymentModeKubernetes = "kubernetes"
)

// k3sKubeconfigPath is where K3s writes its admin kubeconfig on the instance.
const k3sKubeconfigPath = "/etc/rancher/k3s/k3s.yaml"

type Options struct {
	DiskImage                  string
	DiskSizeGB                 int
	RootDevice                 string
	MachineFolder              string
	MachineID                  string
	MachineType                string
	VpcID                      string
	SubnetIDs                  []string
	AvailabilityZone           string
	SecurityGroupID            string
	InstanceProfileArn         string
	InstanceTags               string
	Zone                       string
	UseNestedVirtualization    bool
	UseInstanceConnectEndpoint bool
	InstanceConnectEndpointID  string
	UseSpotInstance            bool
	SpotInstanceType           string
	UseSessionManager          bool
	KmsKeyARNForSessionManager string
	UseRoute53Hostnames        bool
	Route53ZoneName            string
	CustomCredentialCommand    string
	AccessKeyID                string
	SecretAccessKey            string
	SessionToken               string

	// User-data hooks executed during instance initialization
	HookPostSSH    string
	HookPostVolume string

	// Optional secondary data volume
	DataVolumeSnapshotID string
	DataVolumeSizeGB     int
	DataVolumeDevice     string
	DataVolumeMountPath  string
	DataVolumeType       string
	DataVolumeID         string // populated at runtime after CreateVolume

	// Deployment mode: "docker" (default) or "kubernetes" (K3s on the instance).
	DeploymentMode string

	// Kubernetes (K3s) mode settings, only relevant when DeploymentMode is kubernetes.
	K3sVersion          string
	K3sChannel          string
	KubernetesNamespace string
}

// IsKubernetesMode reports whether the instance should run a K3s-backed
// Kubernetes devcontainer instead of a plain Docker devcontainer.
func (o *Options) IsKubernetesMode() bool {
	return o.DeploymentMode == DeploymentModeKubernetes
}

// K3sKubeconfigPath returns the path to the K3s admin kubeconfig on the instance.
func (o *Options) K3sKubeconfigPath() string {
	return k3sKubeconfigPath
}

// HasDataVolume reports whether a secondary data volume is configured.
func (o *Options) HasDataVolume() bool {
	return o.DataVolumeSnapshotID != "" || o.DataVolumeSizeGB > 0
}

var strTrue = "true"

func FromEnv(init, withFolder bool) (*Options, error) {
	retOptions := &Options{}

	if err := applyInstanceOptions(retOptions); err != nil {
		return nil, err
	}
	applyConnectivityOptions(retOptions)
	if err := applyDataVolume(retOptions); err != nil {
		return nil, err
	}
	if err := applyDeploymentMode(retOptions); err != nil {
		return nil, err
	}

	if init {
		return retOptions, nil
	}

	return applyMachineIdentity(retOptions, withFolder)
}

func applyInstanceOptions(o *Options) error {
	o.CustomCredentialCommand = os.Getenv(CUSTOM_AWS_CREDENTIAL_COMMAND)
	o.HookPostSSH = os.Getenv(AWS_HOOK_POST_SSH)
	o.HookPostVolume = os.Getenv(AWS_HOOK_POST_VOLUME)

	var err error
	o.MachineType, err = fromEnvOrError(AWS_INSTANCE_TYPE)
	if err != nil {
		return err
	}

	diskSizeGB, err := fromEnvOrError(AWS_DISK_SIZE)
	if err != nil {
		return err
	}
	o.DiskSizeGB, err = strconv.Atoi(diskSizeGB)
	if err != nil {
		return err
	}

	o.DiskImage = os.Getenv(AWS_AMI)
	o.RootDevice = os.Getenv(AWS_ROOT_DEVICE)
	o.VpcID = os.Getenv(AWS_VPC_ID)
	o.AvailabilityZone = os.Getenv(AWS_AVAILABILITY_ZONE)
	o.InstanceTags = os.Getenv(AWS_INSTANCE_TAGS)
	o.InstanceProfileArn = os.Getenv(AWS_INSTANCE_PROFILE_ARN)
	o.Zone = os.Getenv(AWS_REGION)
	o.UseNestedVirtualization = os.Getenv(AWS_USE_NESTED_VIRTUALIZATION) == strTrue
	return nil
}

func applyConnectivityOptions(o *Options) {
	o.SecurityGroupID = os.Getenv(AWS_SECURITY_GROUP_ID)
	o.UseInstanceConnectEndpoint = os.Getenv(AWS_USE_INSTANCE_CONNECT_ENDPOINT) == strTrue
	o.InstanceConnectEndpointID = os.Getenv(AWS_INSTANCE_CONNECT_ENDPOINT_ID)
	o.UseSpotInstance = os.Getenv(AWS_USE_SPOT_INSTANCE) == strTrue
	o.SpotInstanceType = os.Getenv(AWS_SPOT_INSTANCE_TYPE)
	if o.SpotInstanceType == "" {
		o.SpotInstanceType = "persistent"
	}
	o.UseSessionManager = os.Getenv(AWS_USE_SESSION_MANAGER) == strTrue
	o.KmsKeyARNForSessionManager = os.Getenv(AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER)
	o.UseRoute53Hostnames = os.Getenv(AWS_USE_ROUTE53) == strTrue
	o.Route53ZoneName = os.Getenv(AWS_ROUTE53_ZONE_NAME)
	o.AccessKeyID = os.Getenv(AWS_ACCESS_KEY_ID)
	o.SecretAccessKey = os.Getenv(AWS_SECRET_ACCESS_KEY)
	o.SessionToken = os.Getenv(AWS_SESSION_TOKEN)

	if subnetIDs := os.Getenv(AWS_SUBNET_ID); subnetIDs != "" {
		for subnetID := range strings.SplitSeq(subnetIDs, ",") {
			o.SubnetIDs = append(o.SubnetIDs, strings.TrimSpace(subnetID))
		}
	}
}

// applyDataVolume reads and validates the optional secondary data volume settings.
func applyDataVolume(o *Options) error {
	o.DataVolumeSnapshotID = os.Getenv(AWS_DATA_VOLUME_SNAPSHOT_ID)

	o.DataVolumeDevice = envOrDefault(AWS_DATA_VOLUME_DEVICE, "/dev/xvdf")
	if !devicePathRe.MatchString(o.DataVolumeDevice) {
		return fmt.Errorf(
			"invalid %s: must be a valid device path like /dev/xvdf",
			AWS_DATA_VOLUME_DEVICE,
		)
	}

	o.DataVolumeMountPath = envOrDefault(AWS_DATA_VOLUME_MOUNT_PATH, "/data")
	if !mountPathRe.MatchString(o.DataVolumeMountPath) {
		return fmt.Errorf(
			"invalid %s: must be a valid absolute path like /data",
			AWS_DATA_VOLUME_MOUNT_PATH,
		)
	}

	o.DataVolumeType = envOrDefault(AWS_DATA_VOLUME_TYPE, "gp3")

	return applyDataVolumeSize(o)
}

func applyDataVolumeSize(o *Options) error {
	dataVolSize := os.Getenv(AWS_DATA_VOLUME_SIZE)
	if dataVolSize == "" {
		return nil
	}
	size, err := strconv.Atoi(dataVolSize)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", AWS_DATA_VOLUME_SIZE, err)
	}
	if size < 1 {
		return fmt.Errorf("invalid %s: must be at least 1", AWS_DATA_VOLUME_SIZE)
	}
	o.DataVolumeSizeGB = size
	return nil
}

func envOrDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func applyMachineIdentity(o *Options, withFolder bool) (*Options, error) {
	machineID, err := fromEnvOrError("MACHINE_ID")
	if err != nil {
		return nil, err
	}
	o.MachineID = "devsy-" + machineID

	if withFolder {
		o.MachineFolder, err = fromEnvOrError("MACHINE_FOLDER")
		if err != nil {
			return nil, err
		}
	}
	return o, nil
}

// applyDeploymentMode parses and validates the deployment mode and the
// Kubernetes (K3s) settings that apply when the mode is kubernetes.
func applyDeploymentMode(o *Options) error {
	o.DeploymentMode = os.Getenv(AWS_DEPLOYMENT_MODE)
	if o.DeploymentMode == "" {
		o.DeploymentMode = DeploymentModeDocker
	}
	if o.DeploymentMode != DeploymentModeDocker && o.DeploymentMode != DeploymentModeKubernetes {
		return fmt.Errorf(
			"invalid %s: must be %q or %q",
			AWS_DEPLOYMENT_MODE,
			DeploymentModeDocker,
			DeploymentModeKubernetes,
		)
	}

	o.K3sVersion = os.Getenv(AWS_K3S_VERSION)
	o.K3sChannel = os.Getenv(AWS_K3S_CHANNEL)
	o.KubernetesNamespace = os.Getenv(AWS_KUBERNETES_NAMESPACE)
	if o.KubernetesNamespace == "" {
		o.KubernetesNamespace = "devsy"
	}
	return nil
}

func fromEnvOrError(name string) (string, error) {
	val := os.Getenv(name)
	if val == "" {
		return "", fmt.Errorf(
			"couldn't find option %s in environment, please make sure %s is defined",
			name,
			name,
		)
	}

	return val, nil
}
