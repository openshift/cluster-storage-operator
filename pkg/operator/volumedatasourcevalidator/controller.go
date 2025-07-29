package volumedatasourcevalidator

import (
	"context"
	"os"
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/config/leaderelection"
	"github.com/openshift/library-go/pkg/operator/csi/csidrivercontrollerservicecontroller"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/controller/manager"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

const (
	volumeDataSourceValidatorOperatorImage = "VOLUME_DATA_SOURCE_VALIDATOR_IMAGE"
)

// VolumeDataSourceValidatorStarter is a controller that deploys the volume-data-source-validator
// which validates dataSourceRef field in PersistentVolumeClaims.
type VolumeDataSourceValidatorStarter struct {
	controller     manager.ControllerManager
	operatorClient v1helpers.OperatorClient
	versionGetter  status.VersionGetter
	targetVersion  string
	eventRecorder  events.Recorder
	running        bool
}

func NewController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder) factory.Controller {
	c := &VolumeDataSourceValidatorStarter{
		operatorClient: clients.OperatorClient,
		eventRecorder:  eventRecorder.WithComponentSuffix("VolumeDataSourceValidatorStarter"),
	}

	c.controller = c.createVolumeDataSourceValidatorManager(clients)
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
	).ToController("VolumeDataSourceValidatorStarter", eventRecorder)
}

func (c *VolumeDataSourceValidatorStarter) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("VolumeDataSourceValidatorStarter.Sync started")
	defer klog.V(4).Infof("VolumeDataSourceValidatorStarter.Sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	// Always deploy the volume data source validator. It's a standard Kubernetes component
	// required for PVC data source functionality
	if !c.running {
		go c.controller.Start(ctx)
		c.running = true
	}
	return nil
}

func (c *VolumeDataSourceValidatorStarter) createVolumeDataSourceValidatorManager(
	clients *csoclients.Clients) manager.ControllerManager {
	mgr := manager.NewControllerManager()

	staticAssets := []string{
		"volumedatasourcevalidator/01_serviceaccount.yaml",
		"volumedatasourcevalidator/02_clusterrole.yaml",
		"volumedatasourcevalidator/03_clusterrolebinding.yaml",
		"volumedatasourcevalidator/07_volumedatasourcevalidator_crd.yaml",
	}

	volumeDataSourceValidatorName := "VolumeDataSourceValidatorStaticController"
	mgr = mgr.WithController(staticresourcecontroller.NewStaticResourceController(
		volumeDataSourceValidatorName,
		assets.ReadFile,
		staticAssets,
		resourceapply.NewKubeClientHolder(clients.KubeClient).WithAPIExtensionsClient(clients.ExtensionClientSet),
		c.operatorClient,
		c.eventRecorder).AddKubeInformers(clients.KubeInformers), 1)

	deploymentAssets, err := assets.ReadFile("volumedatasourcevalidator/04_deployment.yaml")
	if err != nil {
		panic(err)
	}

	leConfig := leaderelection.LeaderElectionDefaulting(configv1.LeaderElection{}, "default", "default")

	volumeDataSourceValidatorDeploymentController, err := deploymentcontroller.NewDeploymentControllerBuilder(
		"VolumeDataSourceValidatorDeploymentController",
		deploymentAssets,
		c.eventRecorder,
		clients.OperatorClient,
		clients.KubeClient,
		clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().Deployments(),
	).WithManifestHooks(
		c.withReplacerHook(),
		csidrivercontrollerservicecontroller.WithLeaderElectionReplacerHook(leConfig),
	).WithConditions(
		operatorapi.OperatorStatusTypeProgressing,
		operatorapi.OperatorStatusTypeDegraded,
	).ToController()

	mgr = mgr.WithController(volumeDataSourceValidatorDeploymentController, 1)

	return mgr
}

func (c *VolumeDataSourceValidatorStarter) withReplacerHook() deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorapi.OperatorSpec, deployment []byte) ([]byte, error) {
		logLevel := loglevel.LogLevelToVerbosity(spec.LogLevel)
		pairs := []string{
			"${VOLUME_DATA_SOURCE_VALIDATOR_IMAGE}", os.Getenv(volumeDataSourceValidatorOperatorImage),
			"${LOG_LEVEL}", strconv.Itoa(logLevel),
		}

		replacer := strings.NewReplacer(pairs...)
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}
