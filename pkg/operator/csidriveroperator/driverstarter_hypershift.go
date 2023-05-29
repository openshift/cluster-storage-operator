package csidriveroperator

import (
	"bytes"
	"context"
	"time"

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
	storagelister "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/restmapper"
	"k8s.io/klog/v2"
)

// This CSIDriverStarterController starts CSI driver controllers based on the
// underlying cloud and removes it from OLM. It does not install anything by
// itself, only monitors Infrastructure instance and starts individual
// ControllerManagers for the particular cloud. It produces following Conditions:
// CSIDriverStarterDegraded - error checking the Infrastructure
type CSIDriverStarterControllerHyperShift struct {
	guestClient        *csoclients.Clients
	mgmtClient         *csoclients.Clients
	controlNamespace   string
	infraLister        openshiftv1.InfrastructureLister
	featureGateLister  openshiftv1.FeatureGateLister
	csiDriverLister    storagelister.CSIDriverLister
	restMapper         *restmapper.DeferredDiscoveryRESTMapper
	versionGetter      status.VersionGetter
	targetVersion      string
	eventRecorder      events.Recorder
	guestEventRecorder events.Recorder
	controllers        []csiDriverControllerManager
}

type csiDriverControllerManagerHyperShift struct {
	operatorConfig csioperatorclient.CSIOperatorConfig
	// ControllerManager that installs the CSI driver operator and all its
	// objects.
	mgr                manager.ControllerManager
	running            bool
	ctrlRelatedObjects RelatedObjectGetter
}

func NewCSIDriverStarterControllerHypershift(
	guestClients *csoclients.Clients,
	mgmtClients *csoclients.Clients,
	controlNamespace string,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	guestEventRecorder events.Recorder,
	driverConfigs []csioperatorclient.CSIOperatorConfig) factory.Controller {

	c := &CSIDriverStarterControllerHyperShift{
		guestClient:        guestClients,
		mgmtClient:         mgmtClients,
		controlNamespace:   controlNamespace,
		infraLister:        guestClients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		featureGateLister:  guestClients.ConfigInformers.Config().V1().FeatureGates().Lister(),
		csiDriverLister:    guestClients.KubeInformers.InformersFor("").Storage().V1().CSIDrivers().Lister(),
		restMapper:         guestClients.RestMapper,
		versionGetter:      versionGetter,
		targetVersion:      targetVersion,
		guestEventRecorder: guestEventRecorder.WithComponentSuffix("CSIDriverStarter"),
		eventRecorder:      eventRecorder.WithComponentSuffix("CSIDriverStarter"),
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
		mgr, ctrlRelatedObjects := c.createCSIControllerManager(cfg, resyncInterval)
		c.controllers = append(c.controllers, csiDriverControllerManager{
			operatorConfig:     cfg,
			mgr:                mgr,
			running:            false,
			ctrlRelatedObjects: ctrlRelatedObjects,
		})
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(c.guestClient.OperatorClient).WithInformers(
		guestClients.OperatorClient.Informer(),
		guestClients.ConfigInformers.Config().V1().Infrastructures().Informer(),
		guestClients.ConfigInformers.Config().V1().FeatureGates().Informer(),
		guestClients.KubeInformers.InformersFor("").Storage().V1().CSIDrivers().Informer(),
	).ToController("CSIDriverStarter", eventRecorder)
}

func (c *CSIDriverStarterControllerHyperShift) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverStarterController.Sync started")
	defer klog.V(4).Infof("CSIDriverStarterController.Sync finished")

	opSpec, _, _, err := c.guestClient.OperatorClient.GetOperatorState()
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
	featureGateAccessor := createOldIncompatibleFeatureGatesForHypershift(featureGate)

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
			shouldRun, err := shouldRunController(ctrl.operatorConfig, infrastructure, featureGateAccessor, csiDriver)
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
					c.restMapper.Reset()
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

// createOldIncompatibleFeatureGatesForHypershift takes a featuregate and uses 4.13 logic to stitch together the individual
// featuregates.  Eventually hypershift will (hopefully) follow core OCP and produce a central list.
func createOldIncompatibleFeatureGatesForHypershift(fg *configv1.FeatureGate) featuregates.FeatureGate {
	if fg.Spec.FeatureSet == "" {
		return nil
	}
	if fg.Spec.FeatureSet == configv1.CustomNoUpgrade {
		enabled, disabled := []configv1.FeatureGateName{}, []configv1.FeatureGateName{}
		if fg.Spec.CustomNoUpgrade != nil {
			for _, curr := range fg.Spec.CustomNoUpgrade.Enabled {
				enabled = append(enabled, curr)
			}
			for _, curr := range fg.Spec.CustomNoUpgrade.Disabled {
				disabled = append(disabled, curr)
			}
		}
		return featuregates.NewFeatureGate(enabled, disabled)
	}

	enabled, disabled := []configv1.FeatureGateName{}, []configv1.FeatureGateName{}
	gates := configv1.FeatureSets[fg.Spec.FeatureSet]
	for _, curr := range gates.Enabled {
		enabled = append(enabled, curr.FeatureGateAttributes.Name)
	}
	for _, curr := range gates.Disabled {
		disabled = append(disabled, curr.FeatureGateAttributes.Name)
	}
	return featuregates.NewFeatureGate(enabled, disabled)
}

func (c *CSIDriverStarterControllerHyperShift) createCSIControllerManager(
	cfg csioperatorclient.CSIOperatorConfig,
	resyncInterval time.Duration) (manager.ControllerManager, RelatedObjectGetter) {

	manager := manager.NewControllerManager()

	mgmtStaticResourceClient := resourceapply.NewKubeClientHolder(c.mgmtClient.KubeClient).WithDynamicClient(c.mgmtClient.DynamicClient)
	guestStaticResourceClient := resourceapply.NewKubeClientHolder(c.guestClient.KubeClient).WithDynamicClient(c.guestClient.DynamicClient)

	namespacedAssetFunc := namespaceReplacer(assets.ReadFile, "${CONTROLPLANE_NAMESPACE}", c.controlNamespace)

	mgmtStaticResourceController := staticresourcecontroller.NewStaticResourceController(
		cfg.ConditionPrefix+"CSIDriverOperatorMgmtStaticController",
		namespacedAssetFunc, cfg.MgmtStaticAssets, mgmtStaticResourceClient, c.guestClient.OperatorClient, c.eventRecorder).
		AddKubeInformers(c.mgmtClient.KubeInformers).
		AddRESTMapper(c.mgmtClient.RestMapper).
		AddCategoryExpander(c.mgmtClient.CategoryExpander)

	manager = manager.WithController(mgmtStaticResourceController, 1)

	guestStaticResourceController := staticresourcecontroller.NewStaticResourceController(
		cfg.ConditionPrefix+"CSIDriverOperatorGuestStaticController",
		assets.ReadFile, cfg.GuestStaticAssets, guestStaticResourceClient, c.guestClient.OperatorClient, c.guestEventRecorder).
		AddKubeInformers(c.guestClient.KubeInformers).
		AddRESTMapper(c.guestClient.RestMapper).
		AddCategoryExpander(c.guestClient.CategoryExpander)
	manager = manager.WithController(guestStaticResourceController, 1)
	ctrlRelatedObjects := guestStaticResourceController

	crController := NewCSIDriverOperatorCRController(
		cfg.ConditionPrefix,
		c.guestClient,
		cfg,
		c.guestEventRecorder,
		resyncInterval,
	)
	manager = manager.WithController(crController, 1)

	manager = manager.WithController(NewHyperShiftControllerDeployment(
		c.mgmtClient,
		c.guestClient,
		c.controlNamespace,
		cfg,
		c.versionGetter,
		c.targetVersion,
		c.eventRecorder,
		resyncInterval,
	), 1)

	return manager, ctrlRelatedObjects
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
