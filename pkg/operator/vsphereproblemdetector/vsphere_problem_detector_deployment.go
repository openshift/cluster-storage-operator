package vsphereproblemdetector

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
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

const (
	vSphereProblemDetectorOperatorImage = "VSPHERE_PROBLEM_DETECTOR_OPERATOR_IMAGE"
	deploymentControllerName            = "VSphereProblemDetectorDeploymentController"
)

type VSphereProblemDetectorDeploymentController struct {
	operatorClient v1helpers.OperatorClient
	kubeClient     kubernetes.Interface
	versionGetter  status.VersionGetter
	targetVersion  string
	eventRecorder  events.Recorder
	factory        *factory.Factory
}

func NewVSphereProblemDetectorDeploymentController(
	clients *csoclients.Clients,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	resyncInterval time.Duration) factory.Controller {
	f := factory.New()
	f = f.ResyncEvery(resyncInterval)
	f = f.WithSyncDegradedOnError(clients.OperatorClient)
	// Necessary to do initial Sync after the controller starts.
	f = f.WithPostStartHooks(initalSync)
	f = f.WithInformers(
		clients.OperatorClient.Informer(),
		clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().Deployments().Informer())

	c := &VSphereProblemDetectorDeploymentController{
		operatorClient: clients.OperatorClient,
		kubeClient:     clients.KubeClient,
		versionGetter:  versionGetter,
		eventRecorder:  eventRecorder,
		targetVersion:  targetVersion,
		factory:        f,
	}
	return c
}

func (c *VSphereProblemDetectorDeploymentController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("VSphereProblemDetectorDeploymentController sync started")
	defer klog.V(4).Infof("VSphereProblemDetectorDeploymentController sync finished")

	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
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
		Type: deploymentControllerName + operatorapi.OperatorStatusTypeAvailable,
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
		Type: deploymentControllerName + operatorapi.OperatorStatusTypeProgressing,
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
				c.versionGetter.SetVersion(deploymentControllerName, c.targetVersion)
			} else {
				msg := fmt.Sprintf("%d out of %d pods running", deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas)
				deploymentProgressing.Status = operatorapi.ConditionTrue
				deploymentProgressing.Reason = "WaitDeployment"
				deploymentProgressing.Message = msg
			}
		}
	}

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

func (c *VSphereProblemDetectorDeploymentController) Name() string {
	return deploymentControllerName
}

func (c *VSphereProblemDetectorDeploymentController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(deploymentControllerName, c.eventRecorder)
	ctrl.Run(ctx, workers)
}

func (c *VSphereProblemDetectorDeploymentController) getRequiredDeployment(spec *operatorapi.OperatorSpec) *appsv1.Deployment {
	deploymentString := string(generated.MustAsset("vsphere_problem_detector/06_deployment.yaml"))

	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(vSphereProblemDetectorOperatorImage),
	}

	replacer := strings.NewReplacer(pairs...)
	deploymentString = replacer.Replace(deploymentString)
	// Replace log level
	logLevel := getLogLevel(spec.LogLevel)
	deploymentString = strings.ReplaceAll(deploymentString, "${LOG_LEVEL}", strconv.Itoa(logLevel))

	deployment := resourceread.ReadDeploymentV1OrDie([]byte(deploymentString))
	return deployment
}

// factory.PostStartHook to poke newly started controller to resync.
// This is useful if a controller is started later than at CSO startup
// - CSO's CR may have been already processes and there may be no
// event pending in its informers.
func initalSync(ctx context.Context, syncContext factory.SyncContext) error {
	syncContext.Queue().Add(factory.DefaultQueueKey)
	return nil
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
