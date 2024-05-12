package csidriveroperator

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	csoutils "github.com/openshift/cluster-storage-operator/pkg/utils"
)

const (
	deploymentControllerName = "CSIDriverOperatorDeployment"
)

type CommonCSIDeploymentController struct {
	name              string
	operatorClient    v1helpers.OperatorClient
	commonClients     *csoclients.Clients
	csiOperatorConfig csioperatorclient.CSIOperatorConfig
	kubeClient        kubernetes.Interface
	versionGetter     status.VersionGetter
	targetVersion     string
	eventRecorder     events.Recorder
	infraLister       configv1listers.InfrastructureLister
	resyncInterval    time.Duration
	factory           *factory.Factory
}

func (c *CommonCSIDeploymentController) initController(factoryHookFunc func(*factory.Factory)) *factory.Factory {
	f := factory.New()
	f = f.ResyncEvery(c.resyncInterval)
	f = f.WithSyncDegradedOnError(c.operatorClient)
	// Necessary to do initial Sync after the controller starts.
	f = f.WithPostStartHooks(initalSync)
	// Add informers to the factory now, but the actual event handlers
	// are added later in CSIDriverOperatorDeploymentController.Run(),
	// when we're 100% sure the controller is going to start (because it
	// depends on the platform).
	// If we added the event handlers now, all events would pile up in the
	// controller queue, without anything reading it.
	f = f.WithInformers(
		c.commonClients.OperatorClient.Informer())
	factoryHookFunc(f)
	return f
}

func (c *CommonCSIDeploymentController) preCheckSync(
	ctx context.Context,
	syncCtx factory.SyncContext) (bool, *operatorv1.OperatorStatus, *operatorv1.OperatorSpec, error) {

	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return false, opStatus, opSpec, err
	}
	if opSpec.ManagementState != operatorv1.Managed {
		return false, opStatus, opSpec, nil
	}
	return true, opStatus, opSpec, nil
}

func (c *CommonCSIDeploymentController) postSync(ctx context.Context, deployment *appsv1.Deployment) error {
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

	_, _, err := v1helpers.UpdateStatus(
		ctx,
		c.operatorClient,
		updateStatusFn,
		v1helpers.UpdateConditionFn(progressingCondition),
	)
	return err
}

func initCommonDeploymentParams(
	client *csoclients.Clients,
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder) CommonCSIDeploymentController {
	c := CommonCSIDeploymentController{
		name:              csiOperatorConfig.ConditionPrefix,
		operatorClient:    client.OperatorClient,
		kubeClient:        client.KubeClient,
		csiOperatorConfig: csiOperatorConfig,
		commonClients:     client,
		versionGetter:     versionGetter,
		targetVersion:     targetVersion,
		resyncInterval:    resyncInterval,
		eventRecorder:     eventRecorder.WithComponentSuffix(csiOperatorConfig.ConditionPrefix),
		infraLister:       client.ConfigInformers.Config().V1().Infrastructures().Lister(),
	}
	return c
}

// This CSIDriverStarterController installs and syncs CSI driver operator Deployment.
// It replace ${LOG_LEVEL} in the Deployment with current log level.
// It replaces images in the Deployment using  CSIOperatorConfig.ImageReplacer.
// It produces following Conditions:
// <CSI driver name>CSIDriverOperatorDeploymentProgressing
// <CSI driver name>CSIDriverOperatorDeploymentDegraded
// This controller doesn't set the Available condition to avoid prematurely cascading
// up to the clusteroperator CR a potential Available=false. On the other hand it
// does a better in making sure the Degraded condition is properly set if
// Deployment isn't healthy.
type CSIDriverOperatorDeploymentController struct {
	CommonCSIDeploymentController
}

var _ factory.Controller = &CSIDriverOperatorDeploymentController{}

func NewCSIDriverOperatorDeploymentController(
	clients *csoclients.Clients,
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
) factory.Controller {
	c := &CSIDriverOperatorDeploymentController{
		CommonCSIDeploymentController: initCommonDeploymentParams(clients, csiOperatorConfig, resyncInterval, versionGetter, targetVersion, eventRecorder),
	}
	f := c.initController(func(f *factory.Factory) {
		f.WithInformers(
			c.commonClients.KubeInformers.InformersFor(csoclients.CSIOperatorNamespace).Apps().V1().Deployments().Informer(),
			c.commonClients.ConfigInformers.Config().V1().Infrastructures().Informer())
	})
	c.factory = f
	return c
}

func (c *CSIDriverOperatorDeploymentController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverOperatorDeploymentController sync started")
	defer klog.V(4).Infof("CSIDriverOperatorDeploymentController sync finished")

	runSync, opStatus, opSpec, err := c.preCheckSync(ctx, syncCtx)
	if err != nil {
		return err
	}

	if !runSync {
		return nil
	}

	if !olmRemovalComplete(c.csiOperatorConfig, opStatus) {
		// Wait for the OLM driver to be removed first.
		// OLMOperatorRemovalController already reports its own progress, so
		// users know what's going on.
		return nil
	}

	replacers := []*strings.Replacer{sidecarReplacer}
	// Replace images
	if c.csiOperatorConfig.ImageReplacer != nil {
		replacers = append(replacers, c.csiOperatorConfig.ImageReplacer)
	}

	required, err := csoutils.GetRequiredDeployment(c.csiOperatorConfig.DeploymentAsset, opSpec, nil, replacers...)
	if err != nil {
		return fmt.Errorf("failed to generate required Deployment: %s", err)
	}

	requiredCopy := required.DeepCopy()
	err = util.InjectObservedProxyInDeploymentContainers(requiredCopy, opSpec)
	if err != nil {
		return fmt.Errorf("failed to inject proxy data into deployment: %w", err)
	}

	infra, err := c.infraLister.Get(infraConfigName)
	if err != nil {
		return fmt.Errorf("failed to get infrastructure resource: %w", err)
	}
	if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
		requiredCopy.Spec.Template.Spec.NodeSelector = map[string]string{}
	}

	lastGeneration := resourcemerge.ExpectedDeploymentGeneration(requiredCopy, opStatus.Generations)
	deployment, _, err := resourceapply.ApplyDeployment(ctx, c.kubeClient.AppsV1(), c.eventRecorder, requiredCopy, lastGeneration)
	if err != nil {
		return err
	}

	err = c.postSync(ctx, deployment)
	if err != nil {
		return err
	}
	return checkDeploymentHealth(ctx, c.kubeClient.AppsV1(), deployment)
}

func (c *CSIDriverOperatorDeploymentController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(c.Name(), c.eventRecorder)
	ctrl.Run(ctx, workers)
}

func (c *CSIDriverOperatorDeploymentController) Name() string {
	return c.name + deploymentControllerName
}

// TODO: create a common function in library-go
func isProgressing(deployment *appsv1.Deployment) (bool, string) {
	var deploymentExpectedReplicas int32
	if deployment.Spec.Replicas != nil {
		deploymentExpectedReplicas = *deployment.Spec.Replicas
	}

	switch {
	case deployment.Generation != deployment.Status.ObservedGeneration:
		return true, "Waiting for Deployment to act on changes"
	case deployment.Status.UnavailableReplicas > 0:
		return true, "Waiting for Deployment to deploy pods"
	case deployment.Status.UpdatedReplicas < deploymentExpectedReplicas:
		return true, "Waiting for Deployment to update pods"
	case deployment.Status.AvailableReplicas < deploymentExpectedReplicas:
		return true, "Waiting for Deployment to deploy pods"
	}
	return false, ""
}
