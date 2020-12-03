package csioperatorclient

import (
	"context"
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
)

const (
	cloudConfigName = "kube-cloud-config"
	caBundleKey     = "ca-bundle.pem"

	AWSEBSCSIDriverName          = "ebs.csi.aws.com"
	envAWSEBSDriverOperatorImage = "AWS_EBS_DRIVER_OPERATOR_IMAGE"
	envAWSEBSDriverImage         = "AWS_EBS_DRIVER_IMAGE"
)

func GetAWSEBSCSIOperatorConfig(clients *csoclients.Clients, recorder events.Recorder) CSIOperatorConfig {
	caBundleConfigMap := ""
	if isCustomCABundleUsed(clients) {
		caBundleConfigMap = cloudConfigName
	}

	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAWSEBSDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAWSEBSDriverImage),
		"${CA_BUNDLE_CONFIG_MAP}", caBundleConfigMap,
	}

	return CSIOperatorConfig{
		CSIDriverName:   AWSEBSCSIDriverName,
		ConditionPrefix: "AWSEBS",
		Platform:        configv1.AWSPlatformType,
		StaticAssets: []string{
			"csidriveroperators/aws-ebs/01_namespace.yaml",
			"csidriveroperators/aws-ebs/02_sa.yaml",
			"csidriveroperators/aws-ebs/03_role.yaml",
			"csidriveroperators/aws-ebs/04_rolebinding.yaml",
			"csidriveroperators/aws-ebs/05_clusterrole.yaml",
			"csidriveroperators/aws-ebs/06_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/aws-ebs/08_cr.yaml",
		DeploymentAsset: "csidriveroperators/aws-ebs/07_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		ExtraControllers: []factory.Controller{
			newAWSTrustBundleSyncerOrDie(clients, recorder),
		},
		Optional: false,
		/* For reference / experiments only. OpenShift does not support
		   update from OLM-based AWS EBS operator to CVO/CSO one.
		OLMOptions: &OLMOptions{
			OLMOperatorDeploymentName: "aws-ebs-csi-driver-operator",
			OLMPackageName:            "aws-ebs-csi-driver-operator",
			CRResource: schema.GroupVersionResource{
				Group:    "csi.openshift.io",
				Version:  "v1alpha1",
				Resource: "awsebsdrivers",
			},
		},
		*/
	}
}

func newAWSTrustBundleSyncerOrDie(clients *csoclients.Clients, recorder events.Recorder) factory.Controller {
	// sync config map with additional trust bundle to the operator namespace,
	// so the operator can get it as a ConfigMap volume.
	srcConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: csoclients.CloudConfigManagedNamespace,
		Name:      cloudConfigName,
	}
	dstConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: csoclients.CSIOperatorNamespace,
		Name:      cloudConfigName,
	}
	certController := resourcesynccontroller.NewResourceSyncController(
		clients.OperatorClient,
		clients.KubeInformers,
		clients.KubeClient.CoreV1(),
		clients.KubeClient.CoreV1(),
		recorder)
	err := certController.SyncConfigMap(dstConfigMap, srcConfigMap)
	if err != nil {
		// This can fail if provided clients.KubeInformers does not watch requested namespaces,
		// which is programmatic error.
		klog.Fatalf("Failed to start the AWS CA certificate sync controller: %s", err)
	}
	return certController
}

// isCustomCABundleUsed returns true
func isCustomCABundleUsed(clients *csoclients.Clients) bool {
	cloudConfigCM, err := clients.KubeClient.CoreV1().
		ConfigMaps(csoclients.CloudConfigManagedNamespace).
		Get(context.Background(), cloudConfigName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// no cloud config ConfigMap so there is no CA bundle
		return false
	}
	if err != nil {
		klog.Fatalf("Failed to get the %s/%s ConfigMap", csoclients.CloudConfigManagedNamespace, cloudConfigName)
	}
	_, exists := cloudConfigCM.Data[caBundleKey]
	return exists
}
