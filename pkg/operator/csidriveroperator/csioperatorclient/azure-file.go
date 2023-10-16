package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AzureFileDriverName             = "file.csi.azure.com"
	envAzureFileDriverOperatorImage = "AZURE_FILE_DRIVER_OPERATOR_IMAGE"
	envAzureFileDriverImage         = "AZURE_FILE_DRIVER_IMAGE"
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

func GetAzureFileCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAzureFileDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAzureFileDriverImage),
		"${CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE}", os.Getenv(envCCMOperatorImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
	}

	return CSIOperatorConfig{
		CSIDriverName:   AzureFileDriverName,
		ConditionPrefix: "AzureFile",
		Platform:        configv1.AzurePlatformType,
		StatusFilter:    IsNotAzueStackCloud,
		StaticAssets: []string{
			"csidriveroperators/azure-file/03_sa.yaml",
			"csidriveroperators/azure-file/04_role.yaml",
			"csidriveroperators/azure-file/05_rolebinding.yaml",
			"csidriveroperators/azure-file/06_clusterrole.yaml",
			"csidriveroperators/azure-file/07_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/azure-file/09_cr.yaml",
		DeploymentAsset: "csidriveroperators/azure-file/08_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
