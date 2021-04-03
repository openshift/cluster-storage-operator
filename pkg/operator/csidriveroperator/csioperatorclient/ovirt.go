package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	OVirtDriverName             = "csi.ovirt.org"
	envOVirtDriverOperatorImage = "OVIRT_DRIVER_OPERATOR_IMAGE"
	envOVirtDriverImage         = "OVIRT_DRIVER_IMAGE"
)

func GetOVirtCSIOperatorConfig(clients *csoclients.Clients, recorder events.Recorder) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envOVirtDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envOVirtDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   OVirtDriverName,
		ConditionPrefix: "OVirt",
		Platform:        configv1.OvirtPlatformType,
		StaticAssets: []string{
			"csidriveroperators/ovirt/02_sa.yaml",
			"csidriveroperators/ovirt/03_role.yaml",
			"csidriveroperators/ovirt/04_rolebinding.yaml",
			"csidriveroperators/ovirt/05_clusterrole.yaml",
			"csidriveroperators/ovirt/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/ovirt/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/ovirt/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
