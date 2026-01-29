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

func GetPowerVSBlockCSIOperatorConfig(isHypershift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envPowerVSBlockCSIDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envPowerVSBlockCSIDriverImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   PowerVSBlockCSIDriverName,
		ConditionPrefix: "PowerVSBlock",
		Platform:        configv1.PowerVSPlatformType,
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/powervs-block/standalone/01_sa.yaml",
			"csidriveroperators/powervs-block/standalone/02_role.yaml",
			"csidriveroperators/powervs-block/standalone/03_rolebinding.yaml",
			"csidriveroperators/powervs-block/standalone/04_clusterrole.yaml",
			"csidriveroperators/powervs-block/standalone/05_clusterrolebinding.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/powervs-block/standalone/07_cr.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/powervs-block/standalone/06_deployment.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/powervs-block/hypershift/guest/01_sa.yaml",
			"csidriveroperators/powervs-block/hypershift/guest/02_role.yaml",
			"csidriveroperators/powervs-block/hypershift/guest/03_rolebinding.yaml",
			"csidriveroperators/powervs-block/hypershift/guest/04_clusterrole.yaml",
			"csidriveroperators/powervs-block/hypershift/guest/05_clusterrolebinding.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/powervs-block/hypershift/mgmt/01_operator_role.yaml",
			"csidriveroperators/powervs-block/hypershift/mgmt/01_sa.yaml",
			"csidriveroperators/powervs-block/hypershift/mgmt/03_rolebinding.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/powervs-block/hypershift/mgmt/06_deployment.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/powervs-block/hypershift/guest/07_cr.yaml"
	}

	csiDriverConfig.CSIDriverDeploymentName = getCSIDriverDeploymentName(csiDriverConfig.DeploymentAsset)

	return csiDriverConfig
}
