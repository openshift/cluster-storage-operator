package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	GCPPDCSIDriverName              = "pd.csi.storage.gke.io"
	envGCPPDDriverOperatorImage     = "GCP_PD_DRIVER_OPERATOR_IMAGE"
	envGCPPDDriverImage             = "GCP_PD_DRIVER_IMAGE"
	envGCPPDDriverControlPlaneImage = "GCP_PD_DRIVER_CONTROL_PLANE_IMAGE"
)

func GetGCPPDCSIOperatorConfig(isHypershift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envGCPPDDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envGCPPDDriverImage),
		"${DRIVER_CONTROL_PLANE_IMAGE}", os.Getenv(envGCPPDDriverControlPlaneImage),
		"${OPERATOR_IMAGE_VERSION}", os.Getenv(envOperatorImageVersion),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   GCPPDCSIDriverName,
		ConditionPrefix: "GCPPD",
		Platform:        configv1.GCPPlatformType,
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHypershift {
		csiDriverConfig.StandaloneOperatorConfigAsset = "csidriveroperators/gcp-pd/standalone/generated/v1_configmap_gcp-pd-csi-driver-operator-config.yaml"
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/gcp-pd/standalone/generated/v1_service_gcp-pd-csi-driver-operator-metrics.yaml",
			"csidriveroperators/gcp-pd/standalone/generated/v1_serviceaccount_gcp-pd-csi-driver-operator.yaml",
			"csidriveroperators/gcp-pd/standalone/generated/rbac.authorization.k8s.io_v1_role_gcp-pd-csi-driver-operator-role.yaml",
			"csidriveroperators/gcp-pd/standalone/generated/rbac.authorization.k8s.io_v1_rolebinding_gcp-pd-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/gcp-pd/standalone/generated/rbac.authorization.k8s.io_v1_clusterrole_gcp-pd-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/gcp-pd/standalone/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_gcp-pd-csi-driver-operator-clusterrolebinding.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/gcp-pd/standalone/generated/operator.openshift.io_v1_clustercsidriver_pd.csi.storage.gke.io.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/gcp-pd/standalone/generated/apps_v1_deployment_gcp-pd-csi-driver-operator.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/gcp-pd/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrole_gcp-pd-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/gcp-pd/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_gcp-pd-csi-driver-operator-clusterrolebinding.yaml",
			"csidriveroperators/gcp-pd/hypershift/guest/generated/rbac.authorization.k8s.io_v1_role_gcp-pd-csi-driver-operator-role.yaml",
			"csidriveroperators/gcp-pd/hypershift/guest/generated/rbac.authorization.k8s.io_v1_rolebinding_gcp-pd-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/gcp-pd/hypershift/guest/generated/v1_serviceaccount_gcp-pd-csi-driver-operator.yaml",
		}
		csiDriverConfig.MgmtOperatorConfigAsset = "csidriveroperators/gcp-pd/hypershift/mgmt/generated/v1_configmap_gcp-pd-csi-driver-operator-config.yaml"
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/gcp-pd/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_role_gcp-pd-csi-driver-operator-role.yaml",
			"csidriveroperators/gcp-pd/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_rolebinding_gcp-pd-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/gcp-pd/hypershift/mgmt/generated/v1_serviceaccount_gcp-pd-csi-driver-operator.yaml",
			"csidriveroperators/gcp-pd/hypershift/mgmt/generated/v1_service_gcp-pd-csi-driver-operator-metrics.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/gcp-pd/hypershift/mgmt/generated/apps_v1_deployment_gcp-pd-csi-driver-operator.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/gcp-pd/hypershift/guest/generated/operator.openshift.io_v1_clustercsidriver_pd.csi.storage.gke.io.yaml"
	}

	csiDriverConfig.CSIDriverDeploymentName = getCSIDriverDeploymentName(csiDriverConfig.DeploymentAsset)

	return csiDriverConfig
}
