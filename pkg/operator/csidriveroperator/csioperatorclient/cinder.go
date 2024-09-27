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
			"csidriveroperators/openstack-cinder/02_sa.yaml",
			"csidriveroperators/openstack-cinder/03_role.yaml",
			"csidriveroperators/openstack-cinder/04_rolebinding.yaml",
			"csidriveroperators/openstack-cinder/05_clusterrole.yaml",
			"csidriveroperators/openstack-cinder/06_clusterrolebinding.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/openstack-cinder/08_cr.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/openstack-cinder/07_deployment.yaml"
	} else {
		panic("Hypershift unsupported")
	}

	return csiDriverConfig
}
