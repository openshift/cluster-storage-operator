package vsphereproblemdetector

import (
	"context"
	"os"
	"strings"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	csoutils "github.com/openshift/cluster-storage-operator/pkg/utils"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
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

	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(vSphereProblemDetectorOperatorImage),
	}

	replacer := strings.NewReplacer(pairs...)
	required := csoutils.GetRequiredDeployment("vsphere_problem_detector/06_deployment.yaml", opSpec, replacer)
	_, err = csoutils.CreateDeployment(csoutils.DeploymentOptions{
		Required:       required,
		ControllerName: deploymentControllerName,
		OpStatus:       opStatus,
		EventRecorder:  c.eventRecorder,
		KubeClient:     c.kubeClient,
		OperatorClient: c.operatorClient,
		TargetVersion:  c.targetVersion,
		VersionGetter:  c.versionGetter,
		VersionName:    deploymentControllerName,
	})
	return err
}

func (c *VSphereProblemDetectorDeploymentController) Name() string {
	return deploymentControllerName
}

func (c *VSphereProblemDetectorDeploymentController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(deploymentControllerName, c.eventRecorder)
	ctrl.Run(ctx, workers)
}

// factory.PostStartHook to poke newly started controller to resync.
// This is useful if a controller is started later than at CSO startup
// - CSO's CR may have been already processes and there may be no
// event pending in its informers.
func initalSync(ctx context.Context, syncContext factory.SyncContext) error {
	syncContext.Queue().Add(factory.DefaultQueueKey)
	return nil
}
