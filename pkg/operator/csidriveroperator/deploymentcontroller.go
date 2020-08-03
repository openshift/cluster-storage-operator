package csidriveroperator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// This CSIDriverStarterController installs and syncs CSI driver operator Deployment.
// It replace ${LOG_LEVEL} in the Deployment with current log level.
// It replaces images in the Deployment using  CSIOperatorConfig.ImageReplacer.
// It produces following Conditions:
// <CSI driver name>CSIDriverOperatorDeploymentAvailable
// <CSI driver name>CSIDriverOperatorDeploymentProgressing
// <CSI driver name>CSIDriverOperatorDeploymentDegraded
type CSIDriverOperatorDeploymentController struct {
	name              string
	deploymentAsset   string
	operatorClient    v1helpers.OperatorClient
	csiOperatorConfig csioperatorclient.CSIOperatorConfig
	kubeClient        kubernetes.Interface
	versionGetter     status.VersionGetter
	targetVersion     string
	eventRecorder     events.Recorder
	factory           *factory.Factory
}

var _ factory.Controller = &CSIDriverOperatorDeploymentController{}

const (
	deploymentControllerName = "CSIDriverOperatorDeployment"
)

func NewCSIDriverOperatorDeploymentController(
	clients *csoclients.Clients,
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
) factory.Controller {
	f := factory.New()
	f = f.ResyncEvery(resyncInterval)
	f = f.WithSyncDegradedOnError(clients.OperatorClient)
	// Necessary to do initial Sync after the controller starts.
	f = f.WithPostStartHooks(initalSync)
	// Add informers to the factory now, but the actual event handlers
	// are added later in CSIDriverOperatorDeploymentController.Run(),
	// when we're 100% sure the controller is going to start (because it
	// depends on the platform).
	// If we added the event handlers now, all events would pile up in the
	// controller queue, without anything reading it.
	f = f.WithInformers(
		clients.OperatorClient.Informer(),
		clients.KubeInformers.InformersFor(csoclients.CSIOperatorNamespace).Apps().V1().Deployments().Informer())

	c := &CSIDriverOperatorDeploymentController{
		name:              csiOperatorConfig.ConditionPrefix,
		operatorClient:    clients.OperatorClient,
		csiOperatorConfig: csiOperatorConfig,
		kubeClient:        clients.KubeClient,
		versionGetter:     versionGetter,
		targetVersion:     targetVersion,
		eventRecorder:     eventRecorder.WithComponentSuffix(csiOperatorConfig.ConditionPrefix),
		factory:           f,
	}
	return c
}

func (c *CSIDriverOperatorDeploymentController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverOperatorDeploymentController sync started")
	defer klog.V(4).Infof("CSIDriverOperatorDeploymentController sync finished")

	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	if !olmRemovalComplete(c.csiOperatorConfig, opStatus) {
		// Wait for the OLM driver to be removed first.
		// OLMOperatorRemovalController already reports its own progress, so
		// users know what's going on.
		return nil
	}

	required := c.getRequiredDeployment(opSpec)
	lastGeneration := resourcemerge.ExpectedDeploymentGeneration(required, opStatus.Generations)
	deployment, _, err := resourceapply.ApplyDeployment(c.kubeClient.AppsV1(), c.eventRecorder, required, lastGeneration)
	if err != nil {
		// This will set Degraded condition
		return err
	}

	// Available: at least one replica is running
	deploymentAvailable := operatorapi.OperatorCondition{
		Type: c.Name() + operatorapi.OperatorStatusTypeAvailable,
	}
	if deployment.Status.AvailableReplicas > 0 {
		deploymentAvailable.Status = operatorapi.ConditionTrue
	} else {
		deploymentAvailable.Status = operatorapi.ConditionFalse
		deploymentAvailable.Reason = "WaitDeployment"
		deploymentAvailable.Message = "Waiting for a Deployment pod to start"
	}

	// Not progressing: all replicas are at the latest version && Deployment generation matches
	deploymentProgressing := operatorapi.OperatorCondition{
		Type: c.Name() + operatorapi.OperatorStatusTypeProgressing,
	}
	if deployment.Status.ObservedGeneration != deployment.Generation {
		deploymentProgressing.Status = operatorapi.ConditionTrue
		deploymentProgressing.Reason = "NewGeneration"
		msg := fmt.Sprintf("desired generation %d, current generation %d", deployment.Generation, deployment.Status.ObservedGeneration)
		deploymentProgressing.Message = msg
	} else {
		if deployment.Spec.Replicas != nil {
			if deployment.Status.UpdatedReplicas == *deployment.Spec.Replicas {
				deploymentProgressing.Status = operatorapi.ConditionFalse
				// All replicas were updated, set the version
				c.versionGetter.SetVersion(c.name+versionName, c.targetVersion)
			} else {
				msg := fmt.Sprintf("%d out of %d pods running", deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas)
				deploymentProgressing.Status = operatorapi.ConditionTrue
				deploymentProgressing.Reason = "WaitDeployment"
				deploymentProgressing.Message = msg
			}
		}
	}

	resourcemerge.SetDeploymentGeneration(&opStatus.Generations, deployment)
	opStatus.ReadyReplicas = deployment.Status.ReadyReplicas

	updateGenerationFn := func(newStatus *operatorapi.OperatorStatus) error {
		if deployment != nil {
			resourcemerge.SetDeploymentGeneration(&newStatus.Generations, deployment)
		}
		return nil
	}

	if _, _, err := v1helpers.UpdateStatus(c.operatorClient,
		v1helpers.UpdateConditionFn(deploymentAvailable),
		v1helpers.UpdateConditionFn(deploymentProgressing),
		updateGenerationFn,
	); err != nil {
		return err
	}
	return nil
}

func (c *CSIDriverOperatorDeploymentController) getRequiredDeployment(spec *operatorapi.OperatorSpec) *appsv1.Deployment {
	deploymentAsset := c.csiOperatorConfig.DeploymentAsset
	deploymentString := string(generated.MustAsset(deploymentAsset))

	// Replace images
	if c.csiOperatorConfig.ImageReplacer != nil {
		deploymentString = c.csiOperatorConfig.ImageReplacer.Replace(deploymentString)
	}
	deploymentString = sidecarReplacer.Replace(deploymentString)

	// Replace log level
	logLevel := getLogLevel(spec.LogLevel)
	deploymentString = strings.ReplaceAll(deploymentString, "${LOG_LEVEL}", strconv.Itoa(logLevel))

	deployment := resourceread.ReadDeploymentV1OrDie([]byte(deploymentString))
	return deployment
}

func (c *CSIDriverOperatorDeploymentController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(c.Name(), c.eventRecorder)
	ctrl.Run(ctx, workers)
}

func (c *CSIDriverOperatorDeploymentController) Name() string {
	return c.name + deploymentControllerName
}

func getLogLevel(logLevel operatorapi.LogLevel) int {
	switch logLevel {
	case operatorapi.Normal, "":
		return 2
	case operatorapi.Debug:
		return 4
	case operatorapi.Trace:
		return 6
	case operatorapi.TraceAll:
		return 100
	default:
		return 2
	}
}
