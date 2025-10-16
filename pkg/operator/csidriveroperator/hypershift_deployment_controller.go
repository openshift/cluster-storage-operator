package csidriveroperator

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var (
	envHyperShiftImage = os.Getenv("HYPERSHIFT_IMAGE")

	hostedControlPlaneGVR = schema.GroupVersionResource{
		Group:    "hypershift.openshift.io",
		Version:  "v1beta1",
		Resource: "hostedcontrolplanes",
	}
)

func NewHyperShiftControllerDeployment(
	mgtClient *csoclients.Clients,
	guestClient *csoclients.Clients,
	controlNamespace string,
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
) factory.Controller {

	deploymentBytes, err := assets.ReadFile(csiOperatorConfig.DeploymentAsset)
	if err != nil {
		panic(err)
	}

	replacers := getCommonReplacers(csiOperatorConfig)
	namespaceReplacer := strings.NewReplacer("${CONTROLPLANE_NAMESPACE}", controlNamespace)
	hyperShiftImageReplacer := strings.NewReplacer("${HYPERSHIFT_IMAGE}", envHyperShiftImage)
	replacers = append(replacers, namespaceReplacer, hyperShiftImageReplacer)

	manifestHooks, deploymentHooks := getCommonHooks(replacers)

	hostedControlPlaneInformer := mgtClient.DynamicInformer.ForResource(hostedControlPlaneGVR)
	deploymentHooks = append(deploymentHooks,
		getHyperShiftHook(controlNamespace, hostedControlPlaneInformer.Lister()),
		getAROEnvVarsHook(),
		getRunAsUserHook(),
	)

	c, err := deploymentcontroller.NewDeploymentControllerBuilder(
		csiOperatorConfig.ConditionPrefix,
		deploymentBytes,
		eventRecorder,
		guestClient.OperatorClient,
		mgtClient.KubeClient,
		mgtClient.KubeInformers.InformersFor(controlNamespace).Apps().V1().Deployments(),
	).WithConditions(
		operatorv1.OperatorStatusTypeProgressing,
		operatorv1.OperatorStatusTypeDegraded,
	).WithExtraInformers(
		guestClient.ConfigInformers.Config().V1().Infrastructures().Informer(),
	).WithManifestHooks(
		manifestHooks...,
	).WithDeploymentHooks(
		deploymentHooks...,
	).WithPostStartHooks(
		initalSync,
	).ToController()

	if err != nil {
		panic(err)
	}
	return c
}

func getHyperShiftHook(controlNamespace string, hostedControlPlaneLister cache.GenericLister) deploymentcontroller.DeploymentHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		hcp, err := getHostedControlPlane(controlNamespace, hostedControlPlaneLister)
		if err != nil {
			return err
		}
		nodeSelector, err := getHostedControlPlaneNodeSelector(hcp)
		if err != nil {
			return err
		}
		if nodeSelector != nil {
			deployment.Spec.Template.Spec.NodeSelector = nodeSelector
		}

		labels, err := getHostedControlPlaneLabels(hcp)
		if err != nil {
			return err
		}
		for key, value := range labels {
			// don't replace existing labels as they are used in the deployment's labelSelector.
			if _, exist := deployment.Spec.Template.Labels[key]; !exist {
				deployment.Spec.Template.Labels[key] = value
			}
		}

		tolerations, err := getHostedControlPlaneCustomTolerations(hcp)
		if err != nil {
			return err
		}
		deployment.Spec.Template.Spec.Tolerations = append(deployment.Spec.Template.Spec.Tolerations, tolerations...)

		return nil
	}
}

func getAROEnvVarsHook() deploymentcontroller.DeploymentHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		// The existence of the environment variable, ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE, means this is an ARO HCP
		// deployment. We need to pass along additional environment variables for ARO HCP in order to mount the backing
		// certificates, related to the client IDs, in a volume on the azure-disk-csi-controller and
		// azure-file-csi-controller deployments.
		var envVars []corev1.EnvVar
		if os.Getenv("ARO_HCP_SECRET_PROVIDER_CLASS_FOR_DISK") != "" && deployment.Name == "azure-disk-csi-driver-operator" {
			envVars = []corev1.EnvVar{
				{Name: "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_DISK", Value: os.Getenv("ARO_HCP_SECRET_PROVIDER_CLASS_FOR_DISK")},
			}
		}

		if os.Getenv("ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE") != "" && deployment.Name == "azure-file-csi-driver-operator" {
			envVars = []corev1.EnvVar{
				{Name: "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE", Value: os.Getenv("ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE")},
			}
		}

		if len(envVars) > 0 {
			deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env, envVars...)
		}

		return nil
	}
}

func getHostedControlPlaneCustomTolerations(hcp *unstructured.Unstructured) ([]corev1.Toleration, error) {
	var tolerations []corev1.Toleration
	tolerationsArray, tolerationsArrayFound, err := unstructured.NestedFieldCopy(hcp.UnstructuredContent(), "spec", "tolerations")
	if !tolerationsArrayFound {
		return tolerations, nil
	}
	if err != nil {
		return nil, err
	}
	tolerationsArrayConverted, hasConverted := tolerationsArray.([]interface{})
	if !hasConverted {
		return tolerations, nil
	}

	for _, entry := range tolerationsArrayConverted {
		tolerationConverted, hasConverted := entry.(map[string]interface{})
		if hasConverted {
			toleration := corev1.Toleration{}
			raw, ok := tolerationConverted["key"]
			if ok {
				str, isString := raw.(string)
				if isString {
					toleration.Key = str
				}
			}
			raw, ok = tolerationConverted["operator"]
			if ok {
				op, isOperator := raw.(string)
				if isOperator {
					toleration.Operator = corev1.TolerationOperator(op)
				}
			}
			raw, ok = tolerationConverted["value"]
			if ok {
				str, isString := raw.(string)
				if isString {
					toleration.Value = str
				}
			}
			raw, ok = tolerationConverted["effect"]
			if ok {
				effect, isEffect := raw.(string)
				if isEffect {
					toleration.Effect = corev1.TaintEffect(effect)
				}
			}
			raw, ok = tolerationConverted["tolerationSeconds"]
			if ok {
				seconds, isSeconds := raw.(*int64)
				if isSeconds {
					toleration.TolerationSeconds = seconds
				}
			}
			tolerations = append(tolerations, toleration)
		}
	}

	klog.V(4).Infof("Using tolerations %v", tolerations)
	return tolerations, nil
}

func getHostedControlPlaneNodeSelector(hcp *unstructured.Unstructured) (map[string]string, error) {
	nodeSelector, exists, err := unstructured.NestedStringMap(hcp.UnstructuredContent(), "spec", "nodeSelector")
	if !exists {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("Using node selector %v", nodeSelector)
	return nodeSelector, nil
}

func getHostedControlPlaneLabels(hcp *unstructured.Unstructured) (map[string]string, error) {
	labels, exists, err := unstructured.NestedStringMap(hcp.UnstructuredContent(), "spec", "labels")
	if !exists {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("Using labels %v", labels)
	return labels, nil
}

func getHostedControlPlane(controlNamespace string, hostedControlPlaneLister cache.GenericLister) (*unstructured.Unstructured, error) {
	list, err := hostedControlPlaneLister.ByNamespace(controlNamespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no HostedControlPlane found in namespace %s", controlNamespace)
	}
	if len(list) > 1 {
		return nil, fmt.Errorf("more than one HostedControlPlane found in namespace %s", controlNamespace)
	}

	hcp := list[0].(*unstructured.Unstructured)
	if hcp == nil {
		return nil, fmt.Errorf("unknown type of HostedControlPlane found in namespace %s", controlNamespace)
	}
	return hcp, nil
}

// getRunAsUserHook handles the RUN_AS_USER environment variable for Hypershift deployments.
// This is required for deploying control planes on clusters that do not have Security Context Constraints (SCCs), for example AKS.
// If RUN_AS_USER is set, it adds the environment variable to the CSI operator container and sets runAsUser in the pod security context.
func getRunAsUserHook() deploymentcontroller.DeploymentHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		uid := os.Getenv("RUN_AS_USER")
		if uid == "" {
			return nil
		}

		runAsUserValue, err := strconv.ParseInt(uid, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid RUN_AS_USER value %q: must be a valid integer: %w", uid, err)
		}
		if runAsUserValue < 0 {
			return fmt.Errorf("invalid RUN_AS_USER value %q: must be non-negative", uid)
		}

		runAsUserEnvVar := corev1.EnvVar{
			Name:  "RUN_AS_USER",
			Value: uid,
		}

		for i := range deployment.Spec.Template.Spec.Containers {
			if strings.Contains(deployment.Spec.Template.Spec.Containers[i].Name, "csi-driver-operator") {
				deployment.Spec.Template.Spec.Containers[i].Env = append(
					deployment.Spec.Template.Spec.Containers[i].Env,
					runAsUserEnvVar,
				)
				break
			}
		}

		if deployment.Spec.Template.Spec.SecurityContext == nil {
			deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{}
		}
		deployment.Spec.Template.Spec.SecurityContext.RunAsUser = &runAsUserValue

		return nil
	}
}
