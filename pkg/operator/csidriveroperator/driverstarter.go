package csidriveroperator

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/controller/manager"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	storagelister "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog/v2"
)

const (
	infraConfigName       = "cluster"
	featureGateConfigName = "cluster"

	annOpenShiftManaged = "csi.openshift.io/managed"
)

var (
	relatedObjects []configv1.ObjectReference
)

// This CSIDriverStarterController starts CSI driver controllers based on the
// underlying cloud and removes it from OLM. It does not install anything by
// itself, only monitors Infrastructure instance and starts individual
// ControllerManagers for the particular cloud. It produces following Conditions:
// CSIDriverStarterDegraded - error checking the Infrastructure
type CSIDriverStarterController struct {
	operatorClient    *operatorclient.OperatorClient
	infraLister       openshiftv1.InfrastructureLister
	featureGateLister openshiftv1.FeatureGateLister
	csiDriverLister   storagelister.CSIDriverLister
	versionGetter     status.VersionGetter
	targetVersion     string
	eventRecorder     events.Recorder
	controllers       []csiDriverControllerManager
}

type RelatedObjectGetter interface {
	RelatedObjects() ([]configv1.ObjectReference, error)
}

type csiDriverControllerManager struct {
	operatorConfig csioperatorclient.CSIOperatorConfig
	// ControllerManager that installs the CSI driver operator and all its
	// objects.
	mgr                manager.ControllerManager
	running            bool
	ctrlRelatedObjects RelatedObjectGetter
}

func NewCSIDriverStarterController(
	clients *csoclients.Clients,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	driverConfigs []csioperatorclient.CSIOperatorConfig) factory.Controller {
	c := &CSIDriverStarterController{
		operatorClient:    clients.OperatorClient,
		infraLister:       clients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		featureGateLister: clients.ConfigInformers.Config().V1().FeatureGates().Lister(),
		csiDriverLister:   clients.KubeInformers.InformersFor("").Storage().V1().CSIDrivers().Lister(),
		versionGetter:     versionGetter,
		targetVersion:     targetVersion,
		eventRecorder:     eventRecorder.WithComponentSuffix("CSIDriverStarter"),
	}
	relatedObjects = []configv1.ObjectReference{}

	// Populating all CSI driver operator ControllerManagers here simplifies
	// the startup a lot
	// - All necessary informers are populated.
	// - CSO then just Run()s
	// All ControllerManagers are not running at this point! They will be
	// started in sync() when their platform is detected.
	c.controllers = []csiDriverControllerManager{}
	for _, cfg := range driverConfigs {
		mgr, ctrlRelatedObjects := c.createCSIControllerManager(cfg, clients, resyncInterval)
		c.controllers = append(c.controllers, csiDriverControllerManager{
			operatorConfig:     cfg,
			mgr:                mgr,
			running:            false,
			ctrlRelatedObjects: ctrlRelatedObjects,
		})
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.ConfigInformers.Config().V1().Infrastructures().Informer(),
		clients.ConfigInformers.Config().V1().FeatureGates().Informer(),
		clients.KubeInformers.InformersFor("").Storage().V1().CSIDrivers().Informer(),
	).ToController("CSIDriverStarter", eventRecorder)
}

func (c *CSIDriverStarterController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverStarterController.Sync started")
	defer klog.V(4).Infof("CSIDriverStarterController.Sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
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
	featureGate, err := c.featureGateLister.Get(featureGateConfigName)
	if err != nil {
		return err
	}

	// Start controller managers for this platform
	for i := range c.controllers {
		ctrl := &c.controllers[i]

		csiDriver, err := c.csiDriverLister.Get(ctrl.operatorConfig.CSIDriverName)
		if errors.IsNotFound(err) {
			err = nil
			csiDriver = nil
		}
		if err != nil {
			return err
		}

		if !ctrl.running {
			shouldRun, err := shouldRunController(ctrl.operatorConfig, infrastructure, featureGate, csiDriver)
			if err != nil {
				return err
			}
			if !shouldRun {
				continue
			}
			// add static assets
			objs, err := ctrl.ctrlRelatedObjects.RelatedObjects()
			if err != nil {
				return err
			}
			relatedObjects = append(relatedObjects, objs...)
			relatedObjects = append(relatedObjects, configv1.ObjectReference{
				Group:    operatorapi.GroupName,
				Resource: "clustercsidrivers",
				Name:     ctrl.operatorConfig.CSIDriverName,
			})
			klog.V(2).Infof("Starting ControllerManager for %s", ctrl.operatorConfig.ConditionPrefix)
			go ctrl.mgr.Start(ctx)
			ctrl.running = true
		}
	}
	return nil
}

func (c *CSIDriverStarterController) createCSIControllerManager(
	cfg csioperatorclient.CSIOperatorConfig,
	clients *csoclients.Clients,
	resyncInterval time.Duration) (manager.ControllerManager, RelatedObjectGetter) {

	manager := manager.NewControllerManager()

	src := staticresourcecontroller.NewStaticResourceController(
		cfg.ConditionPrefix+"CSIDriverOperatorStaticController",
		assets.ReadFile, cfg.StaticAssets, resourceapply.NewKubeClientHolder(clients.KubeClient), c.operatorClient, c.eventRecorder).
		AddKubeInformers(clients.KubeInformers).
		AddRESTMapper(clients.RestMapper).
		AddCategoryExpander(clients.CategoryExpander)

	manager = manager.WithController(src, 1)
	ctrlRelatedObjects := src

	crController := NewCSIDriverOperatorCRController(
		cfg.ConditionPrefix,
		clients,
		cfg,
		c.eventRecorder,
		resyncInterval,
	)
	manager = manager.WithController(crController, 1)

	manager = manager.WithController(NewCSIDriverOperatorDeploymentController(
		clients,
		cfg,
		c.versionGetter,
		c.targetVersion,
		c.eventRecorder,
		resyncInterval,
	), 1)

	olmRemovalCtrl := NewOLMOperatorRemovalController(cfg, clients, c.eventRecorder, resyncInterval)
	if olmRemovalCtrl != nil {
		manager = manager.WithController(olmRemovalCtrl, 1)
	}

	for i := range cfg.ExtraControllers {
		manager = manager.WithController(cfg.ExtraControllers[i], 1)
	}

	return manager, ctrlRelatedObjects
}

func RelatedObjectFunc() func() (isset bool, objs []configv1.ObjectReference) {
	return func() (isset bool, objs []configv1.ObjectReference) {
		if len(relatedObjects) == 0 {
			return false, relatedObjects
		}
		return true, relatedObjects
	}
}

// shouldRunController returns true, if given CSI driver controller should run.
func shouldRunController(cfg csioperatorclient.CSIOperatorConfig, infrastructure *configv1.Infrastructure, fg *configv1.FeatureGate, csiDriver *storagev1.CSIDriver) (bool, error) {
	// Check the correct platform first, it will filter out most CSI driver operators
	var platform configv1.PlatformType
	if infrastructure.Status.PlatformStatus != nil {
		platform = infrastructure.Status.PlatformStatus.Type
	}
	if cfg.Platform != csioperatorclient.AllPlatforms && cfg.Platform != platform {
		klog.V(5).Infof("Not starting %s: wrong platform %s", cfg.CSIDriverName, platform)
		return false, nil
	}

	if cfg.RequireFeatureGate == "" {
		// This is GA / always enabled operator, always run
		klog.V(5).Infof("Starting %s: it's GA", cfg.CSIDriverName)
		return true, nil
	}

	if !featureGateEnabled(fg, cfg.RequireFeatureGate) {
		klog.V(4).Infof("Not starting %s: feature %s is not enabled", cfg.CSIDriverName, cfg.RequireFeatureGate)
		return false, nil
	}

	if isUnsupportedCSIDriverRunning(cfg, csiDriver) {
		// Some other version of the CSI driver is running, degrade the whole cluster
		return false, fmt.Errorf("detected CSI driver %s that is not provided by OpenShift - please remove it before enabling the OpenShift one", cfg.CSIDriverName)
	}

	// Tech preview operator and tech preview is enabled
	klog.V(5).Infof("Starting %s: feature %s is enabled", cfg.CSIDriverName, cfg.RequireFeatureGate)
	return true, nil
}

// Get list of enabled feature fates from FeatureGate CR.
func getEnabledFeatures(fg *configv1.FeatureGate) []string {
	if fg.Spec.FeatureSet == "" {
		return nil
	}
	if fg.Spec.FeatureSet == configv1.CustomNoUpgrade {
		return fg.Spec.CustomNoUpgrade.Enabled
	}
	gates := configv1.FeatureSets[fg.Spec.FeatureSet]
	if gates == nil {
		return nil
	}
	return gates.Enabled
}

// featureGateEnabled returns true if a given feature is enabled in FeatureGate CR.
func featureGateEnabled(fg *configv1.FeatureGate, feature string) bool {
	enabledFeatures := getEnabledFeatures(fg)
	for _, f := range enabledFeatures {
		if f == feature {
			return true
		}
	}
	return false
}

func isUnsupportedCSIDriverRunning(cfg csioperatorclient.CSIOperatorConfig, csiDriver *storagev1.CSIDriver) bool {
	if csiDriver == nil {
		return false
	}

	if metav1.HasAnnotation(csiDriver.ObjectMeta, annOpenShiftManaged) {
		return false
	}

	return true
}
