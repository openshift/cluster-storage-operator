package vsphereproblemdetector

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
	csoutils "github.com/openshift/cluster-storage-operator/pkg/utils"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	infraLister    openshiftv1.InfrastructureLister
	versionGetter  status.VersionGetter
	targetVersion  string
	eventRecorder  events.Recorder
}

func NewVSphereProblemDetectorDeploymentController(
	clients *csoclients.Clients,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	resyncInterval time.Duration) factory.Controller {
	c := &VSphereProblemDetectorDeploymentController{
		operatorClient: clients.OperatorClient,
		kubeClient:     clients.KubeClient,
		infraLister:    clients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		versionGetter:  versionGetter,
		eventRecorder:  eventRecorder,
		targetVersion:  targetVersion,
	}
	return factory.New().
		WithSync(c.sync).
		WithInformers(
			c.operatorClient.Informer(),
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().Deployments().Informer(),
			clients.ConfigInformers.Config().V1().Infrastructures().Informer()).
		ResyncEvery(resyncInterval).
		WithSyncDegradedOnError(clients.OperatorClient).
		ToController(deploymentControllerName, eventRecorder.WithComponentSuffix("vsphere-problem-detector-deployment"))
}

func (c *VSphereProblemDetectorDeploymentController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("VSphereProblemDetectorDeploymentController sync started")
	defer klog.V(4).Infof("VSphereProblemDetectorDeploymentController sync finished")

	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(vSphereProblemDetectorOperatorImage),
	}

	infrastructure, err := c.infraLister.Get(infraConfigName)
	if err != nil {
		return err
	}

	replacer := strings.NewReplacer(pairs...)
	required, err := csoutils.GetRequiredDeployment("vsphere_problem_detector/07_deployment.yaml", opSpec, replacer)
	if err != nil {
		return fmt.Errorf("failed to generate required Deployment: %s", err)
	}

	requiredCopy, err := util.InjectObservedProxyInDeploymentContainers(required, opSpec)
	if err != nil {
		return fmt.Errorf("failed to inject proxy data into deployment: %w", err)
	}

	if shouldScheduleOnWorkers(infrastructure) {
		requiredCopy.Spec.Template.Spec.NodeSelector = map[string]string{}
	}

	_, err = csoutils.CreateDeployment(ctx, csoutils.DeploymentOptions{
		Required:       requiredCopy,
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

func shouldScheduleOnWorkers(infra *configv1.Infrastructure) bool {
	return infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode
}
