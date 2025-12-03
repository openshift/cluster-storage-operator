package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AzureFileDriverName                  = "file.csi.azure.com"
	envAzureFileDriverOperatorImage      = "AZURE_FILE_DRIVER_OPERATOR_IMAGE"
	envAzureFileDriverImage              = "AZURE_FILE_DRIVER_IMAGE"
	envAzureFileDriverControlPlangeImage = "AZURE_FILE_DRIVER_CONTROL_PLANE_IMAGE"
)

func IsNotAzueStackCloud(status *configv1.InfrastructureStatus, isInstalled bool) bool {
	if status == nil {
		return false
	}
	// Azure File is not supported on Azure StackHub - skip it unless already installed.
	// https://learn.microsoft.com/en-us/azure-stack/user/azure-stack-acs-differences?view=azs-2206#cheat-sheet-storage-differences
	// TODO: remove this StatusFilter if Azure File gets support in the future
	if status.PlatformStatus.Azure.CloudName == configv1.AzureStackCloud && !isInstalled {
		return false
	}
	return true
}

func GetAzureFileCSIOperatorConfig(isHyperShift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAzureFileDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAzureFileDriverImage),
		"${CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE}", os.Getenv(envCCMOperatorImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
		"${DRIVER_CONTROL_PLANE_IMAGE}", os.Getenv(envAzureFileDriverControlPlangeImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   AzureFileDriverName,
		ConditionPrefix: "AzureFile",
		Platform:        configv1.AzurePlatformType,
		StatusFilter:    IsNotAzueStackCloud,
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHyperShift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/azure-file/standalone/generated/v1_serviceaccount_azure-file-csi-driver-operator.yaml",
			"csidriveroperators/azure-file/standalone/generated/rbac.authorization.k8s.io_v1_role_azure-file-csi-driver-operator-role.yaml",
			"csidriveroperators/azure-file/standalone/generated/rbac.authorization.k8s.io_v1_rolebinding_azure-file-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/azure-file/standalone/generated/rbac.authorization.k8s.io_v1_clusterrole_azure-file-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/azure-file/standalone/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_azure-file-csi-driver-operator-clusterrolebinding.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/azure-file/standalone/generated/apps_v1_deployment_azure-file-csi-driver-operator.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/azure-file/standalone/generated/operator.openshift.io_v1_clustercsidriver_file.csi.azure.com.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/azure-file/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrole_azure-file-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/azure-file/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_azure-file-csi-driver-operator-clusterrolebinding.yaml",
			"csidriveroperators/azure-file/hypershift/guest/generated/rbac.authorization.k8s.io_v1_role_azure-file-csi-driver-operator-role.yaml",
			"csidriveroperators/azure-file/hypershift/guest/generated/rbac.authorization.k8s.io_v1_rolebinding_azure-file-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/azure-file/hypershift/guest/generated/v1_serviceaccount_azure-file-csi-driver-operator.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/azure-file/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_role_azure-file-csi-driver-operator-role.yaml",
			"csidriveroperators/azure-file/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_rolebinding_azure-file-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/azure-file/hypershift/mgmt/generated/v1_serviceaccount_azure-file-csi-driver-operator.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/azure-file/hypershift/mgmt/generated/apps_v1_deployment_azure-file-csi-driver-operator.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/azure-file/hypershift/guest/generated/operator.openshift.io_v1_clustercsidriver_file.csi.azure.com.yaml"
	}

	csiDriverConfig.CSIDriverDeploymentName = getCSIDriverDeploymentName(csiDriverConfig.DeploymentAsset)

	return csiDriverConfig
}
