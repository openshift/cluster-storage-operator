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

func GetOpenStackCinderCSIOperatorConfig(clients *csoclients.Clients, recorder events.Recorder) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envOpenStackCinderDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envOpenStackCinderDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   OpenStackCinderDriverName,
		ConditionPrefix: "OpenStackCinder",
		Platform:        configv1.OpenStackPlatformType,
		StaticAssets: []string{
			"csidriveroperators/openstack-cinder/02_sa.yaml",
			"csidriveroperators/openstack-cinder/03_role.yaml",
			"csidriveroperators/openstack-cinder/04_rolebinding.yaml",
			"csidriveroperators/openstack-cinder/05_clusterrole.yaml",
			"csidriveroperators/openstack-cinder/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/openstack-cinder/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/openstack-cinder/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
