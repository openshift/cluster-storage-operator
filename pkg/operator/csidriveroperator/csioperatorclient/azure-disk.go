package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AzureDiskDriverName                 = "disk.csi.azure.com"
	envAzureDiskDriverOperatorImage     = "AZURE_DISK_DRIVER_OPERATOR_IMAGE"
	envAzureDiskDriverImage             = "AZURE_DISK_DRIVER_IMAGE"
	envCCMOperatorImage                 = "CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE"
	envOperatorImageVersion             = "OPERATOR_IMAGE_VERSION"
	envAzureDiskDriverControlPlaneImage = "AZURE_DISK_DRIVER_CONTROL_PLANE_IMAGE"
)

func GetAzureDiskCSIOperatorConfig(isHyperShift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAzureDiskDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAzureDiskDriverImage),
		"${CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE}", os.Getenv(envCCMOperatorImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
		"${DRIVER_CONTROL_PLANE_IMAGE}", os.Getenv(envAzureDiskDriverControlPlaneImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   AzureDiskDriverName,
		ConditionPrefix: "AzureDisk",
		Platform:        configv1.AzurePlatformType,
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHyperShift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/azure-disk/standalone/generated/v1_serviceaccount_azure-disk-csi-driver-operator.yaml",
			"csidriveroperators/azure-disk/standalone/generated/rbac.authorization.k8s.io_v1_role_azure-disk-csi-driver-operator-role.yaml",
			"csidriveroperators/azure-disk/standalone/generated/rbac.authorization.k8s.io_v1_clusterrole_azure-disk-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/azure-disk/standalone/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_azure-disk-csi-driver-operator-clusterrolebinding.yaml",
			"csidriveroperators/azure-disk/standalone/generated/rbac.authorization.k8s.io_v1_rolebinding_azure-disk-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/azure-disk/standalone/generated/v1_service_azure-disk-csi-driver-operator-metrics.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/azure-disk/standalone/generated/apps_v1_deployment_azure-disk-csi-driver-operator.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/azure-disk/standalone/generated/operator.openshift.io_v1_clustercsidriver_disk.csi.azure.com.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/azure-disk/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrole_azure-disk-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/azure-disk/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_azure-disk-csi-driver-operator-clusterrolebinding.yaml",
			"csidriveroperators/azure-disk/hypershift/guest/generated/rbac.authorization.k8s.io_v1_role_azure-disk-csi-driver-operator-role.yaml",
			"csidriveroperators/azure-disk/hypershift/guest/generated/rbac.authorization.k8s.io_v1_rolebinding_azure-disk-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/azure-disk/hypershift/guest/generated/v1_serviceaccount_azure-disk-csi-driver-operator.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/azure-disk/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_role_azure-disk-csi-driver-operator-role.yaml",
			"csidriveroperators/azure-disk/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_rolebinding_azure-disk-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/azure-disk/hypershift/mgmt/generated/v1_serviceaccount_azure-disk-csi-driver-operator.yaml",
			"csidriveroperators/azure-disk/hypershift/mgmt/generated/v1_service_azure-disk-csi-driver-operator-metrics.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/azure-disk/hypershift/mgmt/generated/apps_v1_deployment_azure-disk-csi-driver-operator.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/azure-disk/hypershift/guest/generated/operator.openshift.io_v1_clustercsidriver_disk.csi.azure.com.yaml"
	}

	csiDriverConfig.CSIDriverDeploymentName = getCSIDriverDeploymentName(csiDriverConfig.DeploymentAsset)

	return csiDriverConfig
}
