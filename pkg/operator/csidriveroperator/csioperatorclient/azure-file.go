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

func GetAzureFileCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAzureFileDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAzureFileDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   AzureFileDriverName,
		ConditionPrefix: "AzureFile",
		Platform:        configv1.AzurePlatformType,
		StaticAssets: []string{
			"csidriveroperators/azure-file/03_sa.yaml",
			"csidriveroperators/azure-file/04_role.yaml",
			"csidriveroperators/azure-file/05_rolebinding.yaml",
			"csidriveroperators/azure-file/06_clusterrole.yaml",
			"csidriveroperators/azure-file/07_clusterrolebinding.yaml",
		},
		CRAsset:            "csidriveroperators/azure-file/09_cr.yaml",
		DeploymentAsset:    "csidriveroperators/azure-file/08_deployment.yaml",
		ImageReplacer:      strings.NewReplacer(pairs...),
		AllowDisabled:      false,
		RequireFeatureGate: "CSIDriverAzureFile",
	}
}
