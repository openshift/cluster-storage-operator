package operator

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/cluster-storage-operator/pkg/operator/defaultstorageclass"
	"github.com/openshift/cluster-storage-operator/pkg/operator/snapshotcrd"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
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
		// Manila is in its own namespace due to migration from OLM.
		{Resource: "namespaces", Name: "openshift-manila-csi-driver"},
		{Group: operatorv1.GroupName, Resource: "storages", Name: operatorclient.GlobalConfigName},
		// Sync with operatorv1.CSIDriverName consts!
		{Group: operatorv1.GroupName, Resource: "clustercsidrivers", Name: string(operatorv1.AWSEBSCSIDriver)},
		{Group: operatorv1.GroupName, Resource: "clustercsidrivers", Name: string(operatorv1.OvirtCSIDriver)},
		{Group: operatorv1.GroupName, Resource: "clustercsidrivers", Name: string(operatorv1.ManilaCSIDriver)},
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

	managementStateController := management.NewOperatorManagementStateController(clusterOperatorName, clients.OperatorClient, controllerConfig.EventRecorder)

	// This controller syncs CR.Status.Conditions with the value in the field CR.Spec.ManagementStatus. It only supports Managed state
	management.SetOperatorNotRemovable()

	// This controller syncs the operator log level with the value set in the CR.Spec.OperatorLogLevel
	logLevelController := loglevel.NewClusterOperatorLoggingController(clients.OperatorClient, controllerConfig.EventRecorder)

	klog.Info("Starting the Informers.")

	csoclients.StartInformers(clients, ctx.Done())

	if err := prefillConditions(ctx, clients, csiDriverConfigs); err != nil {
		return err
	}

	klog.Info("Starting the controllers")
	for _, controller := range []interface {
		Run(ctx context.Context, workers int)
	}{
		logLevelController,
		clusterOperatorStatus,
		managementStateController,
		storageClassController,
		snapshotCRDController,
		csiDriverController,
	} {
		go controller.Run(ctx, 1)
	}

	<-ctx.Done()

	return fmt.Errorf("stopped")
}

func populateConfigs(clients *csoclients.Clients, recorder events.Recorder) []csioperatorclient.CSIOperatorConfig {
	return []csioperatorclient.CSIOperatorConfig{
		csioperatorclient.GetAWSEBSCSIOperatorConfig(),
		csioperatorclient.GetOVirtCSIOperatorConfig(clients, recorder),
		csioperatorclient.GetManilaOperatorConfig(clients, recorder),
	}
}

// Fill all missing Available conditions to Unknown.
// This prevents the operator from reporting Available: true, when only one
// of its many controller had a chance to report Available: true, while the
// others did not even start.
// Clients informers must be already started!
func prefillConditions(ctx context.Context, clients *csoclients.Clients, driverConfigs []csioperatorclient.CSIOperatorConfig) error {
	retryInterval := 2 * time.Second // chosen by fair 1d6 roll

	if !cache.WaitForCacheSync(ctx.Done(), clients.OperatorClient.Informer().HasSynced) {
		return fmt.Errorf("Failed to sync OperatorClient informer")
	}

	expectedConditions := []string{
		defaultstorageclass.ConditionsPrefix + operatorv1.OperatorStatusTypeAvailable,
	}
	for _, cfg := range driverConfigs {
		// Using *OperatorCRAvailable condition, because it's the final one
		// that tells if a CSI driver is completely installed or not.
		expectedConditions = append(expectedConditions, csidriveroperator.GetCSIDriverOperatorCRAvailableName(cfg))
	}

	return wait.PollImmediateInfinite(retryInterval, func() (bool, error) {
		// Stop when the context is done
		if err := ctx.Err(); err != nil {
			return false, err
		}

		_, opStatus, ver, err := clients.OperatorClient.GetOperatorState()
		if err != nil {
			// Try again in the next sync
			klog.Warningf("Failed to get Storage CR: %s", err)
			return false, nil
		}

		statusCopy := opStatus.DeepCopy()
		dirty := false
		for _, cndType := range expectedConditions {
			cnd := v1helpers.FindOperatorCondition(opStatus.Conditions, cndType)
			if cnd == nil {
				dirty = true
				klog.V(4).Infof("Added Unknown condition %s", cndType)
				v1helpers.SetOperatorCondition(&statusCopy.Conditions, operatorv1.OperatorCondition{
					Type:   cndType,
					Status: operatorv1.ConditionUnknown,
					Reason: "Startup",
				})
			}
		}

		if !dirty {
			klog.V(2).Info("All conditions already set")
			return true, nil
		}

		_, err = clients.OperatorClient.UpdateOperatorStatus(ver, statusCopy)
		if err != nil {
			// Try again in the next sync
			klog.Warningf("Failed to update Storage CR: %s", err)
			return false, nil
		}
		klog.V(2).Info("Pre-filled all unknown conditions")
		return true, nil
	})
}
