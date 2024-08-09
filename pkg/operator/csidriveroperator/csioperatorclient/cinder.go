package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	OpenStackCinderDriverName             = "cinder.csi.openstack.org"
	envOpenStackCinderDriverOperatorImage = "OPENSTACK_CINDER_DRIVER_OPERATOR_IMAGE"
	envOpenStackCinderDriverImage         = "OPENSTACK_CINDER_DRIVER_IMAGE"
)

func GetOpenStackCinderCSIOperatorConfig(clients *csoclients.Clients, recorder events.Recorder, isHypershift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envOpenStackCinderDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envOpenStackCinderDriverImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   OpenStackCinderDriverName,
		ConditionPrefix: "OpenStackCinder",
		Platform:        configv1.OpenStackPlatformType,

		ImageReplacer: strings.NewReplacer(pairs...),
		AllowDisabled: false,
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/openstack-cinder/standalone/02_sa.yaml",
			"csidriveroperators/openstack-cinder/standalone/03_role.yaml",
			"csidriveroperators/openstack-cinder/standalone/04_rolebinding.yaml",
			"csidriveroperators/openstack-cinder/standalone/05_clusterrole.yaml",
			"csidriveroperators/openstack-cinder/standalone/06_clusterrolebinding.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/openstack-cinder/08_cr.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/openstack-cinder/07_deployment.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/openstack-cinder/hypershift/guest/02_sa.yaml",
			"csidriveroperators/openstack-cinder/hypershift/guest/03_role.yaml",
			"csidriveroperators/openstack-cinder/hypershift/guest/04_rolebinding.yaml",
			"csidriveroperators/openstack-cinder/hypershift/guest/05_clusterrole.yaml",
			"csidriveroperators/openstack-cinder/hypershift/guest/06_clusterrolebinding.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/openstack-cinder/hypershift/mgmt/01_role.yaml",
			"csidriveroperators/openstack-cinder/hypershift/mgmt/01_sa.yaml",
			"csidriveroperators/openstack-cinder/hypershift/mgmt/04_rolebinding.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/openstack-cinder/hypershift/mgmt/07_deployment.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/openstack-cinder/hypershift/guest/08_cr.yaml"
	}

	return csiDriverConfig
}
