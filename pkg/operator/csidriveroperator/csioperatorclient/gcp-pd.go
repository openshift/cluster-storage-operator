package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	GCPPDCSIDriverName          = "pd.csi.storage.gke.io"
	envGCPPDDriverOperatorImage = "GCP_PD_DRIVER_OPERATOR_IMAGE"
	envGCPPDDriverImage         = "GCP_PD_DRIVER_IMAGE"
)

func GetGCPPDCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envGCPPDDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envGCPPDDriverImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
	}

	return CSIOperatorConfig{
		CSIDriverName:   GCPPDCSIDriverName,
		ConditionPrefix: "GCPPD",
		Platform:        configv1.GCPPlatformType,
		StaticAssets: []string{
			"csidriveroperators/gcp-pd/02_sa.yaml",
			"csidriveroperators/gcp-pd/03_role.yaml",
			"csidriveroperators/gcp-pd/04_rolebinding.yaml",
			"csidriveroperators/gcp-pd/05_clusterrole.yaml",
			"csidriveroperators/gcp-pd/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/gcp-pd/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/gcp-pd/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
