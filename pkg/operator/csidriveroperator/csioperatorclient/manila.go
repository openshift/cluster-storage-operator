package csioperatorclient

import (
	"os"
	"strings"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	CloudConfigName = "cloud-provider-config"

	envManilaDriverOperatorImage = "MANILA_DRIVER_OPERATOR_IMAGE"
	envManilaDriverImage         = "MANILA_DRIVER_IMAGE"
	envNFSDriverImage            = "MANILA_NFS_DRIVER_IMAGE"
)

func GetManilaOperatorConfig(clients *csoclients.Clients, recorder events.Recorder) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envManilaDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envManilaDriverImage),
		"${NFS_DRIVER_IMAGE}", os.Getenv(envNFSDriverImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   "manila.csi.openstack.org",
		ConditionPrefix: "Manila",
		Platform:        v1.OpenStackPlatformType,
		StaticAssets: []string{
			"csidriveroperators/manila/02_sa.yaml",
			"csidriveroperators/manila/03_role.yaml",
			"csidriveroperators/manila/04_rolebinding.yaml",
			"csidriveroperators/manila/05_clusterrole.yaml",
			"csidriveroperators/manila/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/manila/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/manila/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   true,
		OLMOptions: &OLMOptions{
			OLMOperatorDeploymentName: "csi-driver-manila-operator",

			OLMPackageName: "manila-csi-driver-operator",
			CRResource: schema.GroupVersionResource{
				Group:    "csi.openshift.io",
				Version:  "v1alpha1",
				Resource: "maniladrivers",
			},
		},
	}
}
