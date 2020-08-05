package csioperatorclient

import (
	"os"
	"strings"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/csi/credentialsrequestcontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	CloudConfigNamespace = "openshift-config"
	CloudConfigName      = "cloud-provider-config"

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
			"csidriveroperators/manila/01_namespace.yaml",
			"csidriveroperators/manila/02_sa.yaml",
			"csidriveroperators/manila/03_role.yaml",
			"csidriveroperators/manila/04_rolebinding.yaml",
			"csidriveroperators/manila/05_clusterrole.yaml",
			"csidriveroperators/manila/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/manila/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/manila/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		ExtraControllers: []factory.Controller{
			newSecretSyncer(clients, recorder),
			newManilaCredentialsRequest(clients, recorder),
		},
		Optional: true,
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

func newSecretSyncer(clients *csoclients.Clients, recorder events.Recorder) factory.Controller {
	// sync config map with OpenStack CA certificate to the operator namespace,
	// so the operator can get it as a ConfigMap volume.
	srcConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: CloudConfigNamespace,
		Name:      CloudConfigName,
	}
	dstConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: csoclients.CSIOperatorNamespace,
		Name:      CloudConfigName,
	}
	certController := resourcesynccontroller.NewResourceSyncController(
		clients.OperatorClient,
		clients.KubeInformers,
		clients.KubeClient.CoreV1(),
		clients.KubeClient.CoreV1(),
		recorder)
	certController.SyncConfigMap(dstConfigMap, srcConfigMap)
	return certController
}

func newManilaCredentialsRequest(clients *csoclients.Clients, recorder events.Recorder) factory.Controller {
	crController := credentialsrequestcontroller.NewCredentialsRequestController(
		"Manila",
		csoclients.CSIOperatorNamespace,
		generated.MustAsset("csidriveroperators/manila/09_credentials.yaml"),
		clients.DynamicClient,
		clients.OperatorClient,
		recorder)
	return crController
}
