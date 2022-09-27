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
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/managementstatecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	rbacv1 "k8s.io/api/rbac/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

func startHyperShiftController(ctx context.Context, controllerConfig *controllercmd.ControllerContext, guestKubeConfig string) error {
	eventRecorder := controllerConfig.EventRecorder
	controlPlaneNamespace := controllerConfig.OperatorNamespace

	mgmtClients, err := csoclients.NewHypershiftMgmtClients(controllerConfig, controlPlaneNamespace, resync)
	if err != nil {
		return err
	}

	guestClients, err := csoclients.NewHypershiftGuestClients(controllerConfig, guestKubeConfig, clusterOperatorName, resync)
	if err != nil {
		return err
	}

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	allControllers := []factory.Controller{}

	// start the storageclass controller in hypershift guest cluster
	storageClassController := defaultstorageclass.NewController(
		guestClients,
		controllerConfig.EventRecorder,
	)
	allControllers = append(allControllers, storageClassController)

	relatedObjects := []configv1.ObjectReference{
		{Resource: "namespaces", Name: operatorNamespace},
		{Resource: "namespaces", Name: csoclients.CSIOperatorNamespace},
		{Group: operatorv1.GroupName, Resource: "storages", Name: operatorclient.GlobalConfigName},
		{Group: rbacv1.GroupName, Resource: "clusterrolebindings", Name: "cluster-storage-operator-role"},
		{Group: sharedresource.GroupName, Resource: "sharedconfigmaps"},
		{Group: sharedresource.GroupName, Resource: "sharedsecrets"},
	}

	// Create all events in the guest cluster.
	// Use name of the operator Deployment in the mgmt cluster + namespace in the guest cluster as the closest
	// approximation of the real involvedObject.
	controllerRef, err := events.GetControllerReferenceForCurrentPod(ctx, mgmtClients.KubeClient, controlPlaneNamespace, nil)
	controllerRef.Namespace = operatorNamespace
	if err != nil {
		klog.Warningf("unable to get owner reference (falling back to namespace): %v", err)
	}
	guestEventRecorder := events.NewKubeRecorder(guestClients.KubeClient.CoreV1().Events(operatorNamespace), clusterOperatorName, controllerRef)

	// start the operator status controller in guest cluster
	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		clusterOperatorName,
		relatedObjects,
		guestClients.ConfigClientSet.ConfigV1(),
		guestClients.ConfigInformers.Config().V1().ClusterOperators(),
		guestClients.OperatorClient,
		versionGetter,
		guestEventRecorder,
	)
	clusterOperatorStatus.WithRelatedObjectsFunc(csidriveroperator.RelatedObjectFunc())

	allControllers = append(allControllers, clusterOperatorStatus)

	csiDriverConfigs := populateConfigs(mgmtClients, controllerConfig.EventRecorder, true /* A hypershift cluster */)
	csiDriverController := csidriveroperator.NewCSIDriverStarterControllerHypershift(
		guestClients,
		mgmtClients,
		controlPlaneNamespace,
		resync,
		versionGetter,
		status.VersionForOperandFromEnv(),
		eventRecorder,
		guestEventRecorder,
		csiDriverConfigs,
	)
	clusterOperatorStatus.WithRelatedObjectsFunc(csidriveroperator.RelatedObjectFunc())
	allControllers = append(allControllers, csiDriverController)

	managementStateController := managementstatecontroller.NewOperatorManagementStateController(clusterOperatorName, guestClients.OperatorClient, guestEventRecorder)

	// This controller syncs CR.Status.Conditions with the value in the field CR.Spec.ManagementStatus. It only supports Managed state
	management.SetOperatorNotRemovable()

	allControllers = append(allControllers, managementStateController)

	// This controller syncs the operator log level with the value set in the CR.Spec.OperatorLogLevel
	logLevelController := loglevel.NewClusterOperatorLoggingController(guestClients.OperatorClient, guestEventRecorder)
	allControllers = append(allControllers, logLevelController)

	// This controller observes a config (proxy for now) and writes it to CR.Spec.ObservedConfig for later use by the operator
	configObserverController := configobservercontroller.NewConfigObserverController(guestClients, guestEventRecorder)
	allControllers = append(allControllers, configObserverController)

	klog.Info("Starting the Informers.")

	csoclients.StartGuestInformers(guestClients, ctx.Done())
	csoclients.StartMgmtInformers(mgmtClients, ctx.Done())

	klog.Info("Starting the controllers")
	for _, c := range allControllers {
		go func(ctrl factory.Controller) {
			defer utilruntime.HandleCrash()
			ctrl.Run(ctx, 1)
		}(c)
	}

	<-ctx.Done()

	return fmt.Errorf("stopped")
}
