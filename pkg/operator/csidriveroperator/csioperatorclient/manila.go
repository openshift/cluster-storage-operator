package csioperatorclient

import (
	"os"
	"strings"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"k8s.io/klog/v2"
)

const (
	CloudConfigName = "cloud-provider-config"

	envManilaDriverOperatorImage = "MANILA_DRIVER_OPERATOR_IMAGE"
	envManilaDriverImage         = "MANILA_DRIVER_IMAGE"
	envNFSDriverImage            = "MANILA_NFS_DRIVER_IMAGE"
)

func GetOpenStackManilaOperatorConfig(isHypershift bool, clients *csoclients.Clients, recorder events.Recorder) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envManilaDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envManilaDriverImage),
		"${NFS_DRIVER_IMAGE}", os.Getenv(envNFSDriverImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   "manila.csi.openstack.org",
		ConditionPrefix: "Manila",
		Platform:        v1.OpenStackPlatformType,
		ImageReplacer:   strings.NewReplacer(pairs...),
		ExtraControllers: []factory.Controller{
			newCertificateSyncerOrDie(clients, recorder),
		},
		AllowDisabled: true,
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/openstack-manila/standalone/generated/v1_namespace_openshift-manila-csi-driver.yaml",
			"csidriveroperators/openstack-manila/standalone/generated/openshift-cluster-csi-drivers_v1_serviceaccount_manila-csi-driver-operator.yaml",
			"csidriveroperators/openstack-manila/standalone/generated/openshift-cluster-csi-drivers_rbac.authorization.k8s.io_v1_role_manila-csi-driver-operator-role.yaml",
			"csidriveroperators/openstack-manila/standalone/generated/openshift-cluster-csi-drivers_rbac.authorization.k8s.io_v1_rolebinding_manila-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/openstack-manila/standalone/generated/rbac.authorization.k8s.io_v1_clusterrole_manila-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/openstack-manila/standalone/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_manila-csi-driver-operator-clusterrolebinding.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/openstack-manila/standalone/generated/default_operator.openshift.io_v1_clustercsidriver_manila.csi.openstack.org.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/openstack-manila/standalone/generated/openshift-cluster-csi-drivers_apps_v1_deployment_manila-csi-driver-operator.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/openstack-manila/hypershift/guest/generated/v1_namespace_openshift-manila-csi-driver.yaml",
			"csidriveroperators/openstack-manila/hypershift/guest/generated/openshift-cluster-csi-drivers_v1_serviceaccount_manila-csi-driver-operator.yaml",
			"csidriveroperators/openstack-manila/hypershift/guest/generated/openshift-cluster-csi-drivers_rbac.authorization.k8s.io_v1_role_manila-csi-driver-operator-role.yaml",
			"csidriveroperators/openstack-manila/hypershift/guest/generated/openshift-cluster-csi-drivers_rbac.authorization.k8s.io_v1_rolebinding_manila-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/openstack-manila/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrole_manila-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/openstack-manila/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_manila-csi-driver-operator-clusterrolebinding.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/openstack-manila/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_rolebinding_manila-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/openstack-manila/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_role_manila-csi-driver-operator-role.yaml",
			"csidriveroperators/openstack-manila/hypershift/mgmt/generated/v1_serviceaccount_manila-csi-driver-operator.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/openstack-manila/hypershift/guest/generated/default_operator.openshift.io_v1_clustercsidriver_manila.csi.openstack.org.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/openstack-manila/hypershift/mgmt/generated/apps_v1_deployment_manila-csi-driver-operator.yaml"
	}

	return csiDriverConfig
}

func newCertificateSyncerOrDie(clients *csoclients.Clients, recorder events.Recorder) factory.Controller {
	// sync config map with OpenStack CA certificate to the operator namespace,
	// so the operator can get it as a ConfigMap volume.
	srcConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: csoclients.CloudConfigNamespace,
		Name:      CloudConfigName,
	}
	dstConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: csoclients.CSIOperatorNamespace,
		Name:      CloudConfigName,
	}
	certController := resourcesynccontroller.NewResourceSyncController(
		"openshift-storage",
		clients.OperatorClient,
		clients.KubeInformers,
		clients.KubeClient.CoreV1(),
		clients.KubeClient.CoreV1(),
		recorder)
	err := certController.SyncConfigMap(dstConfigMap, srcConfigMap)
	if err != nil {
		// This can fail if provided clients.KubeInformers does not watch requested namespaces,
		// which is programmatic error.
		klog.Fatalf("Failed to start Manila CA certificate sync controller: %s", err)
	}
	return certController
}
