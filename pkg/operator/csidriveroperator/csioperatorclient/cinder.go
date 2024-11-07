package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	OpenStackCinderDriverName             = "cinder.csi.openstack.org"
	envOpenStackCinderDriverOperatorImage = "OPENSTACK_CINDER_DRIVER_OPERATOR_IMAGE"
	envOpenStackCinderDriverImage         = "OPENSTACK_CINDER_DRIVER_IMAGE"
)

func GetOpenStackCinderCSIOperatorConfig(isHypershift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envOpenStackCinderDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envOpenStackCinderDriverImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   OpenStackCinderDriverName,
		ConditionPrefix: "OpenStackCinder",
		Platform:        configv1.OpenStackPlatformType,
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/openstack-cinder/standalone/generated/v1_serviceaccount_openstack-cinder-csi-driver-operator.yaml",
			"csidriveroperators/openstack-cinder/standalone/generated/rbac.authorization.k8s.io_v1_role_openstack-cinder-csi-driver-operator-role.yaml",
			"csidriveroperators/openstack-cinder/standalone/generated/rbac.authorization.k8s.io_v1_rolebinding_openstack-cinder-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/openstack-cinder/standalone/generated/rbac.authorization.k8s.io_v1_clusterrole_openstack-cinder-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/openstack-cinder/standalone/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_openstack-cinder-csi-driver-operator-clusterrolebinding.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/openstack-cinder/standalone/generated/operator.openshift.io_v1_clustercsidriver_cinder.csi.openstack.org.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/openstack-cinder/standalone/generated/apps_v1_deployment_openstack-cinder-csi-driver-operator.yaml"
	} else {
		panic("Hypershift unsupported")
	}

	return csiDriverConfig
}
