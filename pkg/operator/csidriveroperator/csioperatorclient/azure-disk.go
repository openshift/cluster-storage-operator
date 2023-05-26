package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AzureDiskDriverName             = "disk.csi.azure.com"
	envAzureDiskDriverOperatorImage = "AZURE_DISK_DRIVER_OPERATOR_IMAGE"
	envAzureDiskDriverImage         = "AZURE_DISK_DRIVER_IMAGE"
	envCCMOperatorImage             = "CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE"
	envOperatorImageVersion         = "OPERATOR_IMAGE_VERSION"
)

func GetAzureDiskCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAzureDiskDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAzureDiskDriverImage),
		"${CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE}", os.Getenv(envCCMOperatorImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
	}

	return CSIOperatorConfig{
		CSIDriverName:   AzureDiskDriverName,
		ConditionPrefix: "AzureDisk",
		Platform:        configv1.AzurePlatformType,
		StaticAssets: []string{
			"csidriveroperators/azure-disk/03_sa.yaml",
			"csidriveroperators/azure-disk/04_role.yaml",
			"csidriveroperators/azure-disk/05_rolebinding.yaml",
			"csidriveroperators/azure-disk/06_clusterrole.yaml",
			"csidriveroperators/azure-disk/07_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/azure-disk/09_cr.yaml",
		DeploymentAsset: "csidriveroperators/azure-disk/08_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
