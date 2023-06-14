package operator

import (
	"context"
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/api/sharedresource"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator"
	"github.com/openshift/cluster-storage-operator/pkg/operator/defaultstorageclass"
	"github.com/openshift/cluster-storage-operator/pkg/operator/snapshotcrd"
	"github.com/openshift/cluster-storage-operator/pkg/operator/vsphereproblemdetector"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/managementstatecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	rbacv1 "k8s.io/api/rbac/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"time"
)

func startControllerStandAlone(ctx context.Context, controllerConfig *controllercmd.ControllerContext) error {
	clients, err := csoclients.NewClients(controllerConfig, resync)
	if err != nil {
		return err
	}

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	desiredVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"

	// By default, this will exit(0) the process if the featuregates ever change to a different set of values.
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		clients.ConfigInformers.Config().V1().ClusterVersions(), clients.ConfigInformers.Config().V1().FeatureGates(),
		controllerConfig.EventRecorder,
	)
	go featureGateAccessor.Run(ctx)
	go clients.ConfigInformers.Start(ctx.Done())

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

	storageClassController := defaultstorageclass.NewController(
		clients,
		controllerConfig.EventRecorder,
	)

	countStorageClasses(storageClassController, clients)

	// We dont' need this in hypershift guest or mgmt clusters
	snapshotCRDController := snapshotcrd.NewController(
		clients,
		controllerConfig.EventRecorder,
	)

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
		clients.ConfigClientSet.ConfigV1(),
		clients.ConfigInformers.Config().V1().ClusterOperators(),
		clients.OperatorClient,
		versionGetter,
		controllerConfig.EventRecorder,
	)

	csiDriverConfigs := populateConfigs(clients, controllerConfig.EventRecorder, false /* not a hypershift cluster */)
	csiDriverController := csidriveroperator.NewCSIDriverStarterController(
		clients,
		featureGates,
		resync,
		versionGetter,
		status.VersionForOperandFromEnv(),
		controllerConfig.EventRecorder,
		csiDriverConfigs)
	clusterOperatorStatus.WithRelatedObjectsFunc(csidriveroperator.RelatedObjectFunc())

	vsphereProblemDetector := vsphereproblemdetector.NewVSphereProblemDetectorStarter(
		clients,
		resync,
		versionGetter,
		status.VersionForOperandFromEnv(),
		controllerConfig.EventRecorder)

	managementStateController := managementstatecontroller.NewOperatorManagementStateController(clusterOperatorName, clients.OperatorClient, controllerConfig.EventRecorder)

	// This controller syncs CR.Status.Conditions with the value in the field CR.Spec.ManagementStatus. It only supports Managed state
	management.SetOperatorNotRemovable()

	// This controller syncs the operator log level with the value set in the CR.Spec.OperatorLogLevel
	logLevelController := loglevel.NewClusterOperatorLoggingController(clients.OperatorClient, controllerConfig.EventRecorder)

	// This controller observes a config (proxy for now) and writes it to CR.Spec.ObservedConfig for later use by the operator
	configObserverController := configobservercontroller.NewConfigObserverController(clients, controllerConfig.EventRecorder)

	klog.Info("Starting the Informers.")

	csoclients.StartInformers(clients, ctx.Done())

	klog.Info("Starting the controllers")
	for _, c := range []factory.Controller{
		logLevelController,
		clusterOperatorStatus,
		managementStateController,
		configObserverController,
		storageClassController,
		snapshotCRDController,
		csiDriverController,
		vsphereProblemDetector,
	} {
		go func(ctrl factory.Controller) {
			defer utilruntime.HandleCrash()
			ctrl.Run(ctx, 1)
		}(c)
	}

	<-ctx.Done()

	return fmt.Errorf("stopped")
}
