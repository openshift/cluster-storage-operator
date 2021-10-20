package csioperatorclient

import (
	"os"
	"strings"
)

const (
	SharedResourceDriverName             = "csi.sharedresource.openshift.io"
	envSharedResourceDriverOperatorImage = "SHARED_RESOURCE_DRIVER_OPERATOR_IMAGE"
	envSharedResourceDriverImage         = "SHARED_RESOURCE_DRIVER_IMAGE"
)

func GetSharedResourceCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envSharedResourceDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envSharedResourceDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   SharedResourceDriverName,
		ConditionPrefix: "SHARES",
		Platform:        AllPlatforms,
		StaticAssets: []string{
			"csidriveroperators/shared-resource/02_sa.yaml",
			"csidriveroperators/shared-resource/03_role.yaml",
			"csidriveroperators/shared-resource/04_rolebinding.yaml",
			"csidriveroperators/shared-resource/05_clusterrole.yaml",
			"csidriveroperators/shared-resource/06_clusterrolebinding.yaml",
			"csidriveroperators/shared-resource/07_role_config.yaml",
			"csidriveroperators/shared-resource/08_rolebinding_config.yaml",
		},
		CRAsset:            "csidriveroperators/shared-resource/10_cr.yaml",
		DeploymentAsset:    "csidriveroperators/shared-resource/09_deployment.yaml",
		ImageReplacer:      strings.NewReplacer(pairs...),
		AllowDisabled:      false,
		RequireFeatureGate: "CSIDriverSharedResource",
	}
}
