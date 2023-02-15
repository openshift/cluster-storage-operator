package csioperatorclient

import (
	"os"
	"strings"
)

const (
	SharedResourceDriverName             = "csi.sharedresource.openshift.io"
	envSharedResourceDriverOperatorImage = "SHARED_RESOURCE_DRIVER_OPERATOR_IMAGE"
	envSharedResourceDriverImage         = "SHARED_RESOURCE_DRIVER_IMAGE"
	envSharedResourceDriverWebhookImage  = "SHARED_RESOURCE_DRIVER_WEBHOOK_IMAGE"
)

func GetSharedResourceCSIOperatorConfig(isHypershift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envSharedResourceDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envSharedResourceDriverImage),
		"${WEBHOOK_IMAGE}", os.Getenv(envSharedResourceDriverWebhookImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:      SharedResourceDriverName,
		ConditionPrefix:    "SHARES",
		Platform:           AllPlatforms,
		ImageReplacer:      strings.NewReplacer(pairs...),
		AllowDisabled:      false,
		RequireFeatureGate: "",
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/shared-resource/standalone/02_sa.yaml",
			"csidriveroperators/shared-resource/standalone/03_role.yaml",
			"csidriveroperators/shared-resource/standalone/04_rolebinding.yaml",
			"csidriveroperators/shared-resource/standalone/05_clusterrole.yaml",
			"csidriveroperators/shared-resource/standalone/06_clusterrolebinding.yaml",
			"csidriveroperators/shared-resource/standalone/07_role_config.yaml",
			"csidriveroperators/shared-resource/standalone/08_rolebinding_config.yaml",
			"csidriveroperators/shared-resource/standalone/11_metrics_service.yaml",
		}
		csiDriverConfig.ServiceMonitorAsset = "csidriveroperators/shared-resource/standalone/12_servicemonitor.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/shared-resource/standalone/10_cr.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/shared-resource/standalone/09_deployment.yaml"
	} else {
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/shared-resource/hypershift/mgmt/02_sa.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/shared-resource/hypershift/mgmt/09_deployment.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/shared-resource/hypershift/guest/10_cr.yaml"
	}

	return csiDriverConfig
}
