package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AlibabaDiskCSIDriverName          = "diskplugin.csi.alibabacloud.com"
	envAlibabaDiskDriverOperatorImage = "ALIBABA_DISK_DRIVER_OPERATOR_IMAGE"
	envAlibabaCloudDriverImage        = "ALIBABA_CLOUD_DRIVER_IMAGE"
)

func GetAlibabaDiskCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAlibabaDiskDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAlibabaCloudDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   AlibabaDiskCSIDriverName,
		ConditionPrefix: "AlibabaDisk",
		Platform:        configv1.AlibabaCloudPlatformType,
		StaticAssets: []string{
			"csidriveroperators/alibaba-disk/02_sa.yaml",
			"csidriveroperators/alibaba-disk/03_role.yaml",
			"csidriveroperators/alibaba-disk/04_rolebinding.yaml",
			"csidriveroperators/alibaba-disk/05_clusterrole.yaml",
			"csidriveroperators/alibaba-disk/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/alibaba-disk/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/alibaba-disk/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
