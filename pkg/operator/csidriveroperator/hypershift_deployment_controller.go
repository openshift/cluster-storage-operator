package csidriveroperator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	csoutils "github.com/openshift/cluster-storage-operator/pkg/utils"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var _ factory.Controller = &HyperShiftDeploymentController{}

var (
	envHyperShiftImage = os.Getenv("HYPERSHIFT_IMAGE")

	hostedControlPlaneGVR = schema.GroupVersionResource{
		Group:    "hypershift.openshift.io",
		Version:  "v1beta1",
		Resource: "hostedcontrolplanes",
	}
)

// This HyperShiftDeploymentController installs and syncs CSI driver operator Deployment.
// It replace ${LOG_LEVEL} in the Deployment with current log level.
// It replaces images in the Deployment using  CSIOperatorConfig.ImageReplacer.
// It produces following Conditions:
// <CSI driver name>CSIDriverOperatorDeploymentProgressing
// <CSI driver name>CSIDriverOperatorDeploymentDegraded
// This controller doesn't set the Available condition to avoid prematurely cascading
// up to the clusteroperator CR a potential Available=false. On the other hand it
// does a better in making sure the Degraded condition is properly set if the
// Deployment isn't healthy.
type HyperShiftDeploymentController struct {
	CommonCSIDeploymentController
	mgmtClient               *csoclients.Clients
	controlNamespace         string
	hostedControlPlaneLister cache.GenericLister
}

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
	hostedControlPlaneInformer := mgtClient.DynamicInformer.ForResource(hostedControlPlaneGVR)
	c := &HyperShiftDeploymentController{
		CommonCSIDeploymentController: initCommonDeploymentParams(
			guestClient,
			csiOperatorConfig,
			resyncInterval,
			versionGetter,
			targetVersion,
			eventRecorder,
		),
		mgmtClient:               mgtClient,
		controlNamespace:         controlNamespace,
		hostedControlPlaneLister: hostedControlPlaneInformer.Lister(),
	}
	f := c.initController(func(f *factory.Factory) {
		f.WithInformers(
			c.mgmtClient.KubeInformers.InformersFor(controlNamespace).Apps().V1().Deployments().Informer(),
			hostedControlPlaneInformer.Informer(),
		)
	})
	c.factory = f
	return c
}

func (c *HyperShiftDeploymentController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverOperatorDeploymentController sync started")
	defer klog.V(4).Infof("CSIDriverOperatorDeploymentController sync finished")

	runSync, opStatus, opSpec, err := c.preCheckSync(ctx, syncCtx)
	if err != nil {
		return err
	}

	if !runSync {
		return nil
	}

	replacers := []*strings.Replacer{sidecarReplacer}
	// Replace images
	if c.csiOperatorConfig.ImageReplacer != nil {
		replacers = append(replacers, c.csiOperatorConfig.ImageReplacer)
	}

	namespaceReplacer := strings.NewReplacer("${CONTROLPLANE_NAMESPACE}", c.controlNamespace)
	hyperShiftImageReplacer := strings.NewReplacer("${HYPERSHIFT_IMAGE}", envHyperShiftImage)
	replacers = append(replacers, namespaceReplacer)
	replacers = append(replacers, hyperShiftImageReplacer)

	nodeSelector, err := c.getHostedControlPlaneNodeSelector()
	if err != nil {
		return err
	}

	tolerations, err := c.getHostedControlPlaneCustomTolerations()
	if err != nil {
		return err
	}

	required, err := csoutils.GetRequiredDeployment(c.csiOperatorConfig.DeploymentAsset, opSpec, nodeSelector, tolerations, replacers...)
	if err != nil {
		return fmt.Errorf("failed to generate required Deployment: %s", err)
	}

	requiredCopy := required.DeepCopy()
	err = util.InjectObservedProxyInDeploymentContainers(requiredCopy, opSpec)
	if err != nil {
		return fmt.Errorf("failed to inject proxy data into deployment: %w", err)
	}

	lastGeneration := resourcemerge.ExpectedDeploymentGeneration(requiredCopy, opStatus.Generations)
	deployment, _, err := resourceapply.ApplyDeployment(ctx, c.mgmtClient.KubeClient.AppsV1(), c.eventRecorder, requiredCopy, lastGeneration)
	if err != nil {
		return err
	}
	err = c.postSync(ctx, deployment)
	if err != nil {
		return err
	}

	return checkDeploymentHealth(ctx, c.mgmtClient.KubeClient.AppsV1(), deployment)
}

func (c *HyperShiftDeploymentController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(c.Name(), c.eventRecorder)
	ctrl.Run(ctx, workers)
}

func (c *HyperShiftDeploymentController) Name() string {
	return c.name + deploymentControllerName
}

func (c *HyperShiftDeploymentController) getHostedControlPlaneCustomTolerations() ([]corev1.Toleration, error) {
	hcp, err := c.getHostedControlPlane()
	if err != nil {
		return nil, err
	}

	var tolerations []corev1.Toleration
	tolerationsArray, tolerationsArrayFound, err := unstructured.NestedFieldCopy(hcp.UnstructuredContent(), "spec", "tolerations")
	if !tolerationsArrayFound {
		return tolerations, nil
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

func (c *HyperShiftDeploymentController) getHostedControlPlaneNodeSelector() (map[string]string, error) {
	hcp, err := c.getHostedControlPlane()
	if err != nil {
		return nil, err
	}
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

func (c *HyperShiftDeploymentController) getHostedControlPlane() (*unstructured.Unstructured, error) {
	list, err := c.hostedControlPlaneLister.ByNamespace(c.controlNamespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no HostedControlPlane found in namespace %s", c.controlNamespace)
	}
	if len(list) > 1 {
		return nil, fmt.Errorf("more than one HostedControlPlane found in namespace %s", c.controlNamespace)
	}

	hcp := list[0].(*unstructured.Unstructured)
	if hcp == nil {
		return nil, fmt.Errorf("unknown type of HostedControlPlane found in namespace %s", c.controlNamespace)
	}
	return hcp, nil
}
