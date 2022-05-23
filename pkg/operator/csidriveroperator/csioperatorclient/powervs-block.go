package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	PowerVSBlockCSIDriverName             = "powervs.csi.ibm.com"
	envPowerVSBlockCSIDriverOperatorImage = "POWERVS_BLOCK_CSI_DRIVER_OPERATOR_IMAGE"
	envPowerVSBlockCSIDriverImage         = "POWERVS_BLOCK_CSI_DRIVER_IMAGE"
)

func GetPowerVSBlockCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envPowerVSBlockCSIDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envPowerVSBlockCSIDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   PowerVSBlockCSIDriverName,
		ConditionPrefix: "PowerVSBlock",
		Platform:        configv1.PowerVSPlatformType,
		StaticAssets: []string{
			"csidriveroperators/powervs-block/01_sa.yaml",
			"csidriveroperators/powervs-block/02_role.yaml",
			"csidriveroperators/powervs-block/03_rolebinding.yaml",
			"csidriveroperators/powervs-block/04_clusterrole.yaml",
			"csidriveroperators/powervs-block/05_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/powervs-block/07_cr.yaml",
		DeploymentAsset: "csidriveroperators/powervs-block/06_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
