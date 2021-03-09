package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AzureDiskCSIDriverName          = "disk.csi.azure.com"
	envAzureDiskDriverOperatorImage = "AZURE_DISK_DRIVER_OPERATOR_IMAGE"
	envAzureDiskDriverImage         = "AZURE_DISK_DRIVER_IMAGE"
)

func GetAzureDiskCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAzureDiskDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAzureDiskDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   AzureDiskCSIDriverName,
		ConditionPrefix: "AzureDisk",
		Platform:        configv1.AzurePlatformType,
		StaticAssets: []string{
			"csidriveroperators/azure-disk/01_namespace.yaml",
			"csidriveroperators/azure-disk/02_sa.yaml",
			"csidriveroperators/azure-disk/03_role.yaml",
			"csidriveroperators/azure-disk/04_rolebinding.yaml",
			"csidriveroperators/azure-disk/05_clusterrole.yaml",
			"csidriveroperators/azure-disk/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/azure-disk/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/azure-disk/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		Optional:        false,
	}
}
