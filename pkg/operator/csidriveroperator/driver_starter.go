package csidriveroperator

import (
	"bytes"
	"context"
	"fmt"
	"time"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/controller/manager"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	storagelister "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/restmapper"
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

type driverInterface interface {
	initController([]csioperatorclient.CSIOperatorConfig, driverInterface) factory.Controller
	addExtraControllersToManager(manager.ControllerManager, csioperatorclient.CSIOperatorConfig)
	sync(ctx context.Context, syncCtx factory.SyncContext) error
}

type driverStarterCommon struct {
	commonClients   *csoclients.Clients
	resyncInterval  time.Duration
	infraLister     openshiftv1.InfrastructureLister
	featureGates    featuregates.FeatureGate
	csiDriverLister storagelister.CSIDriverLister
	restMapper      *restmapper.DeferredDiscoveryRESTMapper
	versionGetter   status.VersionGetter
	targetVersion   string
	eventRecorder   events.Recorder
	controllers     []csiDriverControllerManager
}

type standAloneDriverStarter struct {
	driverStarterCommon
}

type hypershiftDriverStarter struct {
	driverStarterCommon
	mgmtClient          *csoclients.Clients
	mgmtEventRecorder   events.Recorder
	controllerNamespace string
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

func initCommonStarterParams(
	client *csoclients.Clients,
	featureGates featuregates.FeatureGate,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder) driverStarterCommon {
	c := driverStarterCommon{
		commonClients:  client,
		versionGetter:  versionGetter,
		targetVersion:  targetVersion,
		resyncInterval: resyncInterval,
		featureGates:   featureGates,
		eventRecorder:  eventRecorder.WithComponentSuffix("CSIDriverStarter"),
	}
	return c
}

func (dsrc *driverStarterCommon) createInformers() {
	dsrc.infraLister = dsrc.commonClients.ConfigInformers.Config().V1().Infrastructures().Lister()
	dsrc.csiDriverLister = dsrc.commonClients.KubeInformers.InformersFor("").Storage().V1().CSIDrivers().Lister()
	dsrc.restMapper = dsrc.commonClients.RestMapper
}

func (dsrc *driverStarterCommon) initController(
	driverConfigs []csioperatorclient.CSIOperatorConfig, vStarter driverInterface) factory.Controller {
	dsrc.createInformers()
	relatedObjects = []configv1.ObjectReference{}

	// Populating all CSI driver operator ControllerManagers here simplifies
	// the startup a lot
	// - All necessary informers are populated.
	// - CSO then just Run()s
	// All ControllerManagers are not running at this point! They will be
	// started in sync() when their platform is detected.
	dsrc.controllers = []csiDriverControllerManager{}
	for _, cfg := range driverConfigs {
		mgr, ctrlRelatedObjects := dsrc.createCSIControllerManager(cfg)
		vStarter.addExtraControllersToManager(mgr, cfg)
		dsrc.controllers = append(dsrc.controllers, csiDriverControllerManager{
			operatorConfig:     cfg,
			mgr:                mgr,
			running:            false,
			ctrlRelatedObjects: ctrlRelatedObjects,
		})
	}

	return factory.New().WithSync(dsrc.sync).WithSyncDegradedOnError(dsrc.commonClients.OperatorClient).WithInformers(
		dsrc.commonClients.OperatorClient.Informer(),
		dsrc.commonClients.ConfigInformers.Config().V1().Infrastructures().Informer(),
		dsrc.commonClients.ConfigInformers.Config().V1().FeatureGates().Informer(),
		dsrc.commonClients.KubeInformers.InformersFor("").Storage().V1().CSIDrivers().Informer(),
	).ToController("CSIDriverStarter", dsrc.eventRecorder)
}

func (dsrc *driverStarterCommon) createCSIControllerManager(cfg csioperatorclient.CSIOperatorConfig) (manager.ControllerManager, RelatedObjectGetter) {
	manager := manager.NewControllerManager()
	clients := dsrc.commonClients

	staticResourceClients := resourceapply.NewKubeClientHolder(clients.KubeClient).WithDynamicClient(clients.DynamicClient)
	src := staticresourcecontroller.NewStaticResourceController(
		cfg.ConditionPrefix+"CSIDriverOperatorStaticController",
		assets.ReadFile, cfg.StaticAssets, staticResourceClients, dsrc.commonClients.OperatorClient, dsrc.eventRecorder).
		AddKubeInformers(clients.KubeInformers).
		AddRESTMapper(clients.RestMapper).
		AddCategoryExpander(clients.CategoryExpander)

	manager = manager.WithController(src, 1)
	ctrlRelatedObjects := src

	crController := NewCSIDriverOperatorCRController(
		cfg.ConditionPrefix,
		clients,
		cfg,
		dsrc.eventRecorder,
		dsrc.resyncInterval,
	)
	manager = manager.WithController(crController, 1)

	return manager, ctrlRelatedObjects
}

func (dsrc *driverStarterCommon) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverStarterController.Sync started")
	defer klog.V(4).Infof("CSIDriverStarterController.Sync finished")

	opSpec, _, _, err := dsrc.commonClients.OperatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	infrastructure, err := dsrc.infraLister.Get(infraConfigName)
	if err != nil {
		return err
	}
	// Start controller managers for this platform
	for i := range dsrc.controllers {
		ctrl := &dsrc.controllers[i]

		csiDriver, err := dsrc.csiDriverLister.Get(ctrl.operatorConfig.CSIDriverName)
		if errors.IsNotFound(err) {
			err = nil
			csiDriver = nil
		}
		if err != nil {
			return err
		}

		if !ctrl.running {
			shouldRun, err := shouldRunController(ctrl.operatorConfig, infrastructure, dsrc.featureGates, csiDriver)
			if err != nil {
				return err
			}
			if !shouldRun {
				continue
			}
			// add static assets
			objs, err := ctrl.ctrlRelatedObjects.RelatedObjects()
			if err != nil {
				if isNoMatchError(err) {
					// RESTMapper NoResourceMatch / NoKindMatch errors are cached. Reset the cache to get fresh results on the next sync.
					dsrc.restMapper.Reset()
				}
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

func NewStandaloneDriverStarter(
	clients *csoclients.Clients,
	featureGates featuregates.FeatureGate,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	driverConfigs []csioperatorclient.CSIOperatorConfig) (factory.Controller, *standAloneDriverStarter) {

	c := &standAloneDriverStarter{
		initCommonStarterParams(clients, featureGates, resyncInterval, versionGetter, targetVersion, eventRecorder),
	}

	return c.initController(driverConfigs, c), c
}

func (s *standAloneDriverStarter) addExtraControllersToManager(manager manager.ControllerManager, cfg csioperatorclient.CSIOperatorConfig) {
	manager = manager.WithController(NewCSIDriverOperatorDeploymentController(
		s.commonClients,
		cfg,
		s.versionGetter,
		s.targetVersion,
		s.eventRecorder,
		s.resyncInterval,
	), 1)

	olmRemovalCtrl := NewOLMOperatorRemovalController(cfg, s.commonClients, s.eventRecorder, s.resyncInterval)
	if olmRemovalCtrl != nil {
		manager = manager.WithController(olmRemovalCtrl, 1)
	}

	if cfg.ServiceMonitorAsset != "" {
		manager = manager.WithController(staticresourcecontroller.NewStaticResourceController(
			cfg.ConditionPrefix+"CSIDriverOperatorServiceMonitorController",
			assets.ReadFile,
			[]string{cfg.ServiceMonitorAsset},
			(&resourceapply.ClientHolder{}).WithDynamicClient(s.commonClients.DynamicClient),
			s.commonClients.OperatorClient,
			s.eventRecorder,
		).WithIgnoreNotFoundOnCreate(), 1)
	}

	for i := range cfg.ExtraControllers {
		manager = manager.WithController(cfg.ExtraControllers[i], 1)
	}
}

func NewHypershiftDriverStarter(
	clients *csoclients.Clients,
	mgmtClients *csoclients.Clients,
	fg featuregates.FeatureGate,
	controlNamespace string,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	mgmtEventRecorder events.Recorder,
	driverConfigs []csioperatorclient.CSIOperatorConfig) (factory.Controller, *hypershiftDriverStarter) {

	c := &hypershiftDriverStarter{
		initCommonStarterParams(clients, fg, resyncInterval, versionGetter, targetVersion, eventRecorder),
		mgmtClients,
		mgmtEventRecorder,
		controlNamespace,
	}

	return c.initController(driverConfigs, c), c
}

func (h *hypershiftDriverStarter) addExtraControllersToManager(manager manager.ControllerManager, cfg csioperatorclient.CSIOperatorConfig) {
	mgmtStaticResourceClient := resourceapply.NewKubeClientHolder(h.mgmtClient.KubeClient).WithDynamicClient(h.mgmtClient.DynamicClient)
	namespacedAssetFunc := namespaceReplacer(assets.ReadFile, "${CONTROLPLANE_NAMESPACE}", h.controllerNamespace)

	mgmtStaticResourceController := staticresourcecontroller.NewStaticResourceController(
		cfg.ConditionPrefix+"CSIDriverOperatorMgmtStaticController",
		namespacedAssetFunc, cfg.MgmtStaticAssets, mgmtStaticResourceClient, h.commonClients.OperatorClient, h.eventRecorder).
		AddKubeInformers(h.mgmtClient.KubeInformers).
		AddRESTMapper(h.mgmtClient.RestMapper).
		AddCategoryExpander(h.mgmtClient.CategoryExpander)

	manager = manager.WithController(mgmtStaticResourceController, 1)

	manager = manager.WithController(NewHyperShiftControllerDeployment(
		h.mgmtClient,
		h.commonClients,
		h.controllerNamespace,
		cfg,
		h.versionGetter,
		h.targetVersion,
		h.eventRecorder,
		h.resyncInterval,
	), 1)
}

func namespaceReplacer(assetFunc resourceapply.AssetFunc, placeholder, namespace string) resourceapply.AssetFunc {
	return func(name string) ([]byte, error) {
		asset, err := assetFunc(name)
		if err != nil {
			return asset, err
		}
		asset = bytes.ReplaceAll(asset, []byte(placeholder), []byte(namespace))
		return asset, nil
	}
}

// shouldRunController returns true, if given CSI driver controller should run.
func shouldRunController(cfg csioperatorclient.CSIOperatorConfig, infrastructure *configv1.Infrastructure, fg featuregates.FeatureGate, csiDriver *storagev1.CSIDriver) (bool, error) {
	// Check the correct platform first, it will filter out most CSI driver operators
	var platform configv1.PlatformType
	if infrastructure.Status.PlatformStatus != nil {
		platform = infrastructure.Status.PlatformStatus.Type
	}
	if cfg.Platform != csioperatorclient.AllPlatforms && cfg.Platform != platform {
		klog.V(5).Infof("Not starting %s: wrong platform %s", cfg.CSIDriverName, platform)
		return false, nil
	}

	if cfg.StatusFilter != nil && !cfg.StatusFilter(&infrastructure.Status) {
		klog.V(5).Infof("Not starting %s: StatusFilter returned false", cfg.CSIDriverName)
		return false, nil
	}

	if cfg.RequireFeatureGate == "" {
		// This is GA / always enabled operator, always run
		klog.V(5).Infof("Starting %s: it's GA", cfg.CSIDriverName)
		return true, nil
	}

	knownFeatures := sets.New[configv1.FeatureGateName](fg.KnownFeatures()...)
	if !knownFeatures.Has(cfg.RequireFeatureGate) || !fg.Enabled(cfg.RequireFeatureGate) {
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

func RelatedObjectFunc() func() (isset bool, objs []configv1.ObjectReference) {
	return func() (isset bool, objs []configv1.ObjectReference) {
		if len(relatedObjects) == 0 {
			return false, relatedObjects
		}
		return true, relatedObjects
	}
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

func isNoMatchError(err error) bool {
	// ctrlRelatedObjects.RelatedObjects() may return aggregated errors, process that
	if agg, ok := err.(utilerrors.Aggregate); ok {
		errs := agg.Errors()
		for _, err := range errs {
			if meta.IsNoMatchError(err) {
				return true
			}
		}
		return false
	}

	return meta.IsNoMatchError(err)
}
