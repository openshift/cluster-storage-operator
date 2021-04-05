package operator

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/cluster-storage-operator/pkg/operator/defaultstorageclass"
	"github.com/openshift/cluster-storage-operator/pkg/operator/snapshotcrd"
	"github.com/openshift/cluster-storage-operator/pkg/operator/vsphereproblemdetector"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/status"
)

const (
	resync = 20 * time.Minute
)

const (
	operatorNamespace   = "openshift-cluster-storage-operator"
	clusterOperatorName = "storage"
)

func RunOperator(ctx context.Context, controllerConfig *controllercmd.ControllerContext) error {
	clients, err := csoclients.NewClients(controllerConfig, resync)
	if err != nil {
		return err
	}

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	storageClassController := defaultstorageclass.NewController(
		clients,
		controllerConfig.EventRecorder,
	)

	snapshotCRDController := snapshotcrd.NewController(
		clients,
		controllerConfig.EventRecorder,
	)

	relatedObjects := []configv1.ObjectReference{
		{Resource: "namespaces", Name: operatorNamespace},
		{Resource: "namespaces", Name: csoclients.CSIOperatorNamespace},
		{Group: operatorv1.GroupName, Resource: "storages", Name: operatorclient.GlobalConfigName},
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

	csiDriverConfigs := populateConfigs(clients, controllerConfig.EventRecorder)
	csiDriverController := csidriveroperator.NewCSIDriverStarterController(
		clients,
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

	managementStateController := management.NewOperatorManagementStateController(clusterOperatorName, clients.OperatorClient, controllerConfig.EventRecorder)

	// This controller syncs CR.Status.Conditions with the value in the field CR.Spec.ManagementStatus. It only supports Managed state
	management.SetOperatorNotRemovable()

	// This controller syncs the operator log level with the value set in the CR.Spec.OperatorLogLevel
	logLevelController := loglevel.NewClusterOperatorLoggingController(clients.OperatorClient, controllerConfig.EventRecorder)

	// This controller observes a config (proxy for now) and writes it to CR.Spec.ObservedConfig for later use by the operator
	configObserverController := configobservercontroller.NewConfigObserverController(clients, controllerConfig.EventRecorder)

	klog.Info("Starting the Informers.")

	csoclients.StartInformers(clients, ctx.Done())

	klog.Info("Starting the controllers")
	for _, controller := range []interface {
		Run(ctx context.Context, workers int)
	}{
		logLevelController,
		clusterOperatorStatus,
		managementStateController,
		configObserverController,
		storageClassController,
		snapshotCRDController,
		csiDriverController,
		vsphereProblemDetector,
	} {
		go controller.Run(ctx, 1)
	}

	<-ctx.Done()

	return fmt.Errorf("stopped")
}

func populateConfigs(clients *csoclients.Clients, recorder events.Recorder) []csioperatorclient.CSIOperatorConfig {
	return []csioperatorclient.CSIOperatorConfig{
		csioperatorclient.GetAWSEBSCSIOperatorConfig(),
		csioperatorclient.GetGCPPDCSIOperatorConfig(),
		csioperatorclient.GetOpenStackCinderCSIOperatorConfig(clients, recorder),
		csioperatorclient.GetOVirtCSIOperatorConfig(clients, recorder),
		csioperatorclient.GetManilaOperatorConfig(clients, recorder),
	}
}
