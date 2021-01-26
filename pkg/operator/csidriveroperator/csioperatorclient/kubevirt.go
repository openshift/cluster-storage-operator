package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	KubevirtDriverName             = "csi.kubevirt.io"
	envKubevirtDriverOperatorImage = "KUBEVIRT_DRIVER_OPERATOR_IMAGE"
	envKubevirtDriverImage         = "KUBEVIRT_DRIVER_IMAGE"
)

func GetKubevirtCSIOperatorConfig(clients *csoclients.Clients, recorder events.Recorder) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envKubevirtDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envKubevirtDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   KubevirtDriverName,
		ConditionPrefix: "Kubevirt",
		Platform:        configv1.KubevirtPlatformType,
		StaticAssets: []string{
			"csidriveroperators/kubevirt/01_namespace.yaml",
			"csidriveroperators/kubevirt/02_sa.yaml",
			"csidriveroperators/kubevirt/03_role.yaml",
			"csidriveroperators/kubevirt/04_rolebinding.yaml",
			"csidriveroperators/kubevirt/05_clusterrole.yaml",
			"csidriveroperators/kubevirt/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/kubevirt/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/kubevirt/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		Optional:        false,
	}
}
