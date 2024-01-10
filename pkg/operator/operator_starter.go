package operator

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/api/sharedresource"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/cluster-storage-operator/pkg/operator/defaultstorageclass"
	"github.com/openshift/cluster-storage-operator/pkg/operator/vsphereproblemdetector"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/managementstatecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	rbacv1 "k8s.io/api/rbac/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

type OperatorStarter interface {
	StartOperator(ctx context.Context) error
	initClient(ctx context.Context) error
}

type commonStarter struct {
	controllerConfig *controllercmd.ControllerContext

	eventRecorder events.Recorder
	versionGetter status.VersionGetter
	featureGates  featuregates.FeatureGate

	commonClients *csoclients.Clients
	// array of controllers that needs to be started.
	controllers []factory.Controller
}

func (csr *commonStarter) initClient(ctx context.Context) error {
	clients, err := csoclients.NewClients(csr.controllerConfig, resync)
	if err != nil {
		return err
	}
	csr.commonClients = clients
	csr.eventRecorder = csr.controllerConfig.EventRecorder
	return nil
}

func (csr *commonStarter) getFeatureGate(ctx context.Context) error {
	desiredVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"

	// By default, this will exit(0) the process if the featuregates ever change to a different set of values.
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		csr.commonClients.ConfigInformers.Config().V1().ClusterVersions(), csr.commonClients.ConfigInformers.Config().V1().FeatureGates(),
		csr.eventRecorder,
	)
	go featureGateAccessor.Run(ctx)
	go csr.commonClients.ConfigInformers.Start(ctx.Done())

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: knownFeatureGates=%v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		klog.Errorf("timed out waiting for FeatureGate detection")
		return fmt.Errorf("timed out waiting for FeatureGate detection")
	}
	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return err
	}
	csr.featureGates = featureGates
	return nil
}

func (csr *commonStarter) CreateCommonControllers() error {
	csr.versionGetter = status.NewVersionGetter()
	csr.versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	storageClassController := defaultstorageclass.NewController(
		csr.commonClients,
		csr.eventRecorder,
	)
	csr.controllers = append(csr.controllers, storageClassController)

	relatedObjects := []configv1.ObjectReference{
		{Resource: "namespaces", Name: operatorNamespace},
		{Resource: "namespaces", Name: csoclients.CSIOperatorNamespace},
		{Group: operatorv1.GroupName, Resource: "storages", Name: operatorclient.GlobalConfigName},
		{Group: rbacv1.GroupName, Resource: "clusterrolebindings", Name: "cluster-storage-operator-role"},
		{Group: sharedresource.GroupName, Resource: "sharedconfigmaps"},
		{Group: sharedresource.GroupName, Resource: "sharedsecrets"},
	}
	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		clusterOperatorName,
		relatedObjects,
		csr.commonClients.ConfigClientSet.ConfigV1(),
		csr.commonClients.ConfigInformers.Config().V1().ClusterOperators(),
		csr.commonClients.OperatorClient,
		csr.versionGetter,
		csr.eventRecorder,
	).WithVersionRemoval()

	clusterOperatorStatus.WithRelatedObjectsFunc(csidriveroperator.RelatedObjectFunc())
	csr.controllers = append(csr.controllers, clusterOperatorStatus)

	managementStateController := managementstatecontroller.NewOperatorManagementStateController(
		clusterOperatorName, csr.commonClients.OperatorClient, csr.eventRecorder)
	// This controller syncs CR.Status.Conditions with the value in the field CR.Spec.ManagementStatus. It only supports Managed state
	management.SetOperatorNotRemovable()
	csr.controllers = append(csr.controllers, managementStateController)

	// This controller syncs the operator log level with the value set in the CR.Spec.OperatorLogLevel
	logLevelController := loglevel.NewClusterOperatorLoggingController(csr.commonClients.OperatorClient, csr.eventRecorder)
	csr.controllers = append(csr.controllers, logLevelController)

	// This controller observes a config (proxy for now) and writes it to CR.Spec.ObservedConfig for later use by the operator
	configObserverController := configobservercontroller.NewConfigObserverController(csr.commonClients, csr.eventRecorder)
	csr.controllers = append(csr.controllers, configObserverController)
	return nil
}

func (csr *commonStarter) startControllers(ctx context.Context) {
	klog.Info("Starting the controllers")
	for _, c := range csr.controllers {
		go func(ctrl factory.Controller) {
			defer utilruntime.HandleCrash()
			ctrl.Run(ctx, 1)
		}(c)
	}
	<-ctx.Done()
}

type StandaloneStarter struct {
	commonStarter
}

var _ OperatorStarter = &StandaloneStarter{}

func NewStandaloneStarter(controllerConfig *controllercmd.ControllerContext) OperatorStarter {
	ssr := &StandaloneStarter{}
	ssr.controllerConfig = controllerConfig
	return ssr
}

func (ssr *StandaloneStarter) StartOperator(ctx context.Context) error {
	err := ssr.initClient(ctx)
	if err != nil {
		return err
	}
	err = ssr.commonStarter.CreateCommonControllers()
	if err != nil {
		return err
	}

	err = ssr.commonStarter.getFeatureGate(ctx)
	if err != nil {
		return err
	}

	countStorageClasses(ssr.commonClients)

	csiDriverConfigs := ssr.populateConfigs(ssr.commonClients)
	csiDriverController, _ := csidriveroperator.NewStandaloneDriverStarter(
		ssr.commonClients,
		ssr.featureGates,
		resync,
		ssr.versionGetter,
		status.VersionForOperandFromEnv(),
		ssr.eventRecorder,
		csiDriverConfigs)
	ssr.controllers = append(ssr.controllers, csiDriverController)

	vsphereProblemDetector := vsphereproblemdetector.NewVSphereProblemDetectorStarter(
		ssr.commonClients,
		resync,
		ssr.versionGetter,
		status.VersionForOperandFromEnv(),
		ssr.eventRecorder)
	ssr.controllers = append(ssr.controllers, vsphereProblemDetector)

	klog.Info("Starting the Informers.")

	csoclients.StartInformers(ssr.commonClients, ctx.Done())

	ssr.startControllers(ctx)
	return nil
}

func (ssr *StandaloneStarter) populateConfigs(clients *csoclients.Clients) []csioperatorclient.CSIOperatorConfig {
	return []csioperatorclient.CSIOperatorConfig{
		csioperatorclient.GetAWSEBSCSIOperatorConfig(false),
		csioperatorclient.GetGCPPDCSIOperatorConfig(),
		csioperatorclient.GetOpenStackCinderCSIOperatorConfig(clients, ssr.eventRecorder),
		csioperatorclient.GetOVirtCSIOperatorConfig(clients, ssr.eventRecorder),
		csioperatorclient.GetManilaOperatorConfig(clients, ssr.eventRecorder),
		csioperatorclient.GetVMwareVSphereCSIOperatorConfig(),
		csioperatorclient.GetAzureDiskCSIOperatorConfig(),
		csioperatorclient.GetAzureFileCSIOperatorConfig(),
		csioperatorclient.GetSharedResourceCSIOperatorConfig(false),
		csioperatorclient.GetAlibabaDiskCSIOperatorConfig(),
		csioperatorclient.GetIBMVPCBlockCSIOperatorConfig(),
		csioperatorclient.GetPowerVSBlockCSIOperatorConfig(false),
	}
}

type HyperShiftStarter struct {
	commonStarter
	guestKubeConfig string
	mgmtClient      *csoclients.Clients
}

func NewHyperShiftStarter(controllerConfig *controllercmd.ControllerContext, guestKubeConfig string) OperatorStarter {
	hsr := &HyperShiftStarter{}
	hsr.controllerConfig = controllerConfig
	hsr.guestKubeConfig = guestKubeConfig
	return hsr
}

func (hsr *HyperShiftStarter) initClient(ctx context.Context) error {
	controlPlaneNamespace := hsr.controllerConfig.OperatorNamespace

	mgmtClients, err := csoclients.NewHypershiftMgmtClients(hsr.controllerConfig, controlPlaneNamespace, resync)
	if err != nil {
		return err
	}
	hsr.mgmtClient = mgmtClients

	guestClients, err := csoclients.NewHypershiftGuestClients(hsr.controllerConfig, hsr.guestKubeConfig, clusterOperatorName, resync)
	if err != nil {
		return err
	}
	// guestClient is where most resources get initialized in hypershift
	hsr.commonClients = guestClients

	// Create all events in the guest cluster.
	// Use name of the operator Deployment in the mgmt cluster + namespace in the guest cluster as the closest
	// approximation of the real involvedObject.
	controllerRef, err := events.GetControllerReferenceForCurrentPod(ctx, mgmtClients.KubeClient, controlPlaneNamespace, nil)
	controllerRef.Namespace = operatorNamespace
	if err != nil {
		klog.Warningf("unable to get owner reference (falling back to namespace): %v", err)
	}
	guestEventRecorder := events.NewKubeRecorder(guestClients.KubeClient.CoreV1().Events(operatorNamespace), clusterOperatorName, controllerRef)
	hsr.eventRecorder = guestEventRecorder
	return nil
}

func (hsr *HyperShiftStarter) StartOperator(ctx context.Context) error {
	err := hsr.initClient(ctx)
	if err != nil {
		return err
	}
	err = hsr.commonStarter.CreateCommonControllers()
	if err != nil {
		return err
	}

	controlPlaneNamespace := hsr.controllerConfig.OperatorNamespace
	csiDriverConfigs := hsr.populateConfigs(hsr.mgmtClient)

	err = hsr.commonStarter.getFeatureGate(ctx)
	if err != nil {
		return err
	}

	csiDriverController, _ := csidriveroperator.NewHypershiftDriverStarter(
		hsr.commonClients,
		hsr.mgmtClient,
		hsr.featureGates,
		controlPlaneNamespace,
		resync,
		hsr.versionGetter,
		status.VersionForOperandFromEnv(),
		hsr.eventRecorder,
		hsr.controllerConfig.EventRecorder,
		csiDriverConfigs,
	)

	hsr.controllers = append(hsr.controllers, csiDriverController)
	klog.Info("Starting the Informers.")

	csoclients.StartGuestInformers(hsr.commonClients, ctx.Done())
	csoclients.StartMgmtInformers(hsr.mgmtClient, ctx.Done())

	hsr.startControllers(ctx)
	return nil
}

func (hsr *HyperShiftStarter) populateConfigs(clients *csoclients.Clients) []csioperatorclient.CSIOperatorConfig {
	return []csioperatorclient.CSIOperatorConfig{
		csioperatorclient.GetAWSEBSCSIOperatorConfig(true),
		csioperatorclient.GetPowerVSBlockCSIOperatorConfig(true),
		csioperatorclient.GetSharedResourceCSIOperatorConfig(true),
	}
}
