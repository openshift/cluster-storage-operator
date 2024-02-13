package csidriveroperator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	csoutils "github.com/openshift/cluster-storage-operator/pkg/utils"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
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
	name                     string
	guestClient              *csoclients.Clients
	mgmtClient               *csoclients.Clients
	controlNamespace         string
	operatorClient           v1helpers.OperatorClient
	csiOperatorConfig        csioperatorclient.CSIOperatorConfig
	versionGetter            status.VersionGetter
	targetVersion            string
	eventRecorder            events.Recorder
	hostedControlPlaneLister cache.GenericLister
	factory                  *factory.Factory
}

var _ factory.Controller = &HyperShiftDeploymentController{}

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
	hostedControlPlaneInformer := mgtClient.DynamicInformer.ForResource(hostedControlPlaneGVR)
	f := factory.New()
	f = f.ResyncEvery(resyncInterval)
	f = f.WithSyncDegradedOnError(guestClient.OperatorClient)
	// Necessary to do initial Sync after the controller starts.
	f = f.WithPostStartHooks(initalSync)
	// Add informers to the factory now, but the actual event handlers
	// are added later in CSIDriverOperatorDeploymentController.Run(),
	// when we're 100% sure the controller is going to start (because it
	// depends on the platform).
	// If we added the event handlers now, all events would pile up in the
	// controller queue, without anything reading it.
	f = f.WithInformers(
		guestClient.OperatorClient.Informer(),
		mgtClient.KubeInformers.InformersFor(controlNamespace).Apps().V1().Deployments().Informer(),
		hostedControlPlaneInformer.Informer())

	c := &HyperShiftDeploymentController{
		name:                     csiOperatorConfig.ConditionPrefix,
		mgmtClient:               mgtClient,
		guestClient:              guestClient,
		controlNamespace:         controlNamespace,
		operatorClient:           guestClient.OperatorClient,
		csiOperatorConfig:        csiOperatorConfig,
		versionGetter:            versionGetter,
		targetVersion:            targetVersion,
		eventRecorder:            eventRecorder.WithComponentSuffix(csiOperatorConfig.ConditionPrefix),
		factory:                  f,
		hostedControlPlaneLister: hostedControlPlaneInformer.Lister(),
	}
	return c
}

func (c *HyperShiftDeploymentController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverOperatorDeploymentController sync started")
	defer klog.V(4).Infof("CSIDriverOperatorDeploymentController sync finished")

	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorv1.Managed {
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
	required, err := csoutils.GetRequiredDeployment(c.csiOperatorConfig.DeploymentAsset, opSpec, nodeSelector, replacers...)
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

	progressingCondition := operatorv1.OperatorCondition{
		Type:   c.name + operatorv1.OperatorStatusTypeProgressing,
		Status: operatorv1.ConditionFalse,
	}

	if ok, msg := isProgressing(deployment); ok {
		progressingCondition.Status = operatorv1.ConditionTrue
		progressingCondition.Message = msg
		progressingCondition.Reason = "Deploying"
	}

	updateStatusFn := func(newStatus *operatorv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&newStatus.Generations, deployment)
		return nil
	}

	_, _, err = v1helpers.UpdateStatus(
		ctx,
		c.operatorClient,
		updateStatusFn,
		v1helpers.UpdateConditionFn(progressingCondition),
	)

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
