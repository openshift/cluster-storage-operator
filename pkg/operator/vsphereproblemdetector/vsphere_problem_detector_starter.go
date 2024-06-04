package vsphereproblemdetector

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/controller/manager"
	"github.com/openshift/library-go/pkg/operator/csi/csidrivercontrollerservicecontroller"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcehash"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/klog/v2"
)

const (
	infraConfigName                     = "cluster"
	vSphereProblemDetectorOperatorImage = "VSPHERE_PROBLEM_DETECTOR_OPERATOR_IMAGE"
	cloudCredSecretName                 = "vsphere-cloud-credentials"
	metricsCertSecretName               = "vsphere-problem-detector-serving-cert"
	cloudConfigNamespace                = "openshift-config"
)

type VSphereProblemDetectorStarter struct {
	controller     manager.ControllerManager
	operatorClient v1helpers.OperatorClientWithFinalizers
	infraLister    openshiftv1.InfrastructureLister
	versionGetter  status.VersionGetter
	targetVersion  string
	eventRecorder  events.Recorder
	running        bool
}

func NewVSphereProblemDetectorStarter(
	clients *csoclients.Clients,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder) factory.Controller {
	c := &VSphereProblemDetectorStarter{
		operatorClient: clients.OperatorClient,
		infraLister:    clients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		versionGetter:  versionGetter,
		targetVersion:  targetVersion,
		eventRecorder:  eventRecorder.WithComponentSuffix("VSphereProblemDetectorStarter"),
	}
	c.controller = c.createVSphereProblemDetectorManager(clients, resyncInterval)
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.ConfigInformers.Config().V1().Infrastructures().Informer(),
	).ToController("VSphereProblemDetectorStarter", eventRecorder)
}

func (c *VSphereProblemDetectorStarter) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("VSphereProblemDetectorStarter.Sync started")
	defer klog.V(4).Infof("VSphereProblemDetectorStarter.Sync finished")

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

	infrastructure, err := c.infraLister.Get(infraConfigName)
	if err != nil {
		return err
	}

	// Start controller managers for this platform
	var platform configv1.PlatformType
	if infrastructure.Status.PlatformStatus != nil {
		platform = infrastructure.Status.PlatformStatus.Type
	}

	// if not vsphere turn without any error
	if platform != configv1.VSpherePlatformType {
		return nil
	}

	if !c.running {
		go c.controller.Start(ctx)
		c.running = true
	}
	return nil
}

func (c *VSphereProblemDetectorStarter) createVSphereProblemDetectorManager(
	clients *csoclients.Clients,
	resyncInterval time.Duration) manager.ControllerManager {
	mgr := manager.NewControllerManager()

	staticAssets := []string{
		"vsphere_problem_detector/01_sa.yaml",
		"vsphere_problem_detector/02_role.yaml",
		"vsphere_problem_detector/03_rolebinding.yaml",
		"vsphere_problem_detector/04_clusterrole.yaml",
		"vsphere_problem_detector/05_clusterrolebinding.yaml",
		"vsphere_problem_detector/06_configmap.yaml",
		"vsphere_problem_detector/10_service.yaml",
	}

	vSphereProblemDetectorName := "VSphereProblemDetectorStarterStaticController"
	mgr = mgr.WithController(staticresourcecontroller.NewStaticResourceController(
		vSphereProblemDetectorName,
		assets.ReadFile,
		staticAssets,
		resourceapply.NewKubeClientHolder(clients.KubeClient),
		c.operatorClient,
		c.eventRecorder).AddKubeInformers(clients.KubeInformers), 1)

	deploymentAssets, err := assets.ReadFile("vsphere_problem_detector/07_deployment.yaml")
	if err != nil {
		panic(err)
	}

	vSphereProblemDetectorController, err := deploymentcontroller.NewDeploymentControllerBuilder(
		"VSphereProblemDetectorDeploymentController",
		deploymentAssets,
		c.eventRecorder,
		clients.OperatorClient,
		clients.KubeClient,
		clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().Deployments(),
	).WithExtraInformers(
		clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().Secrets().Informer(),
		clients.ConfigInformers.Config().V1().Infrastructures().Informer(),
	).WithManifestHooks(
		c.withReplacerHook(),
	).WithDeploymentHooks(
		csidrivercontrollerservicecontroller.WithControlPlaneTopologyHook(clients.ConfigInformers),
		withProxyHook(),
		// Restart when credentials change to get a quick retest
		csidrivercontrollerservicecontroller.WithSecretHashAnnotationHook(
			csoclients.OperatorNamespace,
			cloudCredSecretName,
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().Secrets(),
		),
		// Restart when serving-cert changes
		csidrivercontrollerservicecontroller.WithSecretHashAnnotationHook(
			csoclients.OperatorNamespace,
			metricsCertSecretName,
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().Secrets(),
		),
		// Restart when cloud config changes to get a quick retest
		c.WithConfigMapHashAnnotationHook(
			cloudConfigNamespace,
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().ConfigMaps(),
		),
	).WithConditions(
		// No Available Condition
		operatorapi.OperatorStatusTypeProgressing,
		operatorapi.OperatorStatusTypeDegraded,
	).ToController()

	if err != nil {
		panic(err)
	}

	mgr = mgr.WithController(vSphereProblemDetectorController, 1)

	mgr = mgr.WithController(newMonitoringController(
		clients,
		c.eventRecorder,
		resyncInterval), 1)

	return mgr
}

func (c *VSphereProblemDetectorStarter) withReplacerHook() deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorapi.OperatorSpec, deployment []byte) ([]byte, error) {
		logLevel := loglevel.LogLevelToVerbosity(spec.LogLevel)
		pairs := []string{
			"${OPERATOR_IMAGE}", os.Getenv(vSphereProblemDetectorOperatorImage),
			"${LOG_LEVEL}", strconv.Itoa(logLevel),
		}

		replacer := strings.NewReplacer(pairs...)
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}

func (c *VSphereProblemDetectorStarter) WithConfigMapHashAnnotationHook(namespace string, cmInformer coreinformers.ConfigMapInformer) deploymentcontroller.DeploymentHookFunc {
	return func(opSpec *operatorapi.OperatorSpec, deployment *appsv1.Deployment) error {
		// Find cloud-config ConfigMap name from Infrastructure
		infra, err := c.infraLister.Get(infraConfigName)
		if err != nil {
			return err
		}
		cloudConfigName := infra.Spec.CloudConfig.Name

		// Compute ConfigMap hash
		inputHashes, err := resourcehash.MultipleObjectHashStringMapForObjectReferenceFromLister(
			cmInformer.Lister(),
			nil,
			resourcehash.NewObjectRef().ForConfigMap().InNamespace(namespace).Named(cloudConfigName),
		)
		if err != nil {
			return fmt.Errorf("invalid dependency reference: %w", err)
		}

		// Add the hash to Deployment annotations
		return addObjectHash(deployment, inputHashes)
	}
}

func addObjectHash(deployment *appsv1.Deployment, inputHashes map[string]string) error {
	if deployment == nil {
		return fmt.Errorf("invalid deployment: %v", deployment)
	}
	if deployment.Annotations == nil {
		deployment.Annotations = map[string]string{}
	}
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	for k, v := range inputHashes {
		annotationKey := fmt.Sprintf("operator.openshift.io/dep-%s", k)
		if len(annotationKey) > 63 {
			hash := sha256.Sum256([]byte(k))
			annotationKey = fmt.Sprintf("operator.openshift.io/dep-%x", hash)
			annotationKey = annotationKey[:63]
		}
		deployment.Annotations[annotationKey] = v
		deployment.Spec.Template.Annotations[annotationKey] = v
	}
	return nil
}

func withProxyHook() deploymentcontroller.DeploymentHookFunc {
	return func(opSpec *operatorapi.OperatorSpec, deployment *appsv1.Deployment) error {
		// Cannot use csidrivercontrollerservicecontroller.WithObservedProxyDeploymentHook here.
		// It expects proxy config at spec.observedConfig.targetcsiconfig.proxy,
		// while CSO uses spec.observedConfig.targetconfig.proxy
		err := util.InjectObservedProxyInDeploymentContainers(deployment, opSpec)
		return err
	}
}
