package defaultstorageclass

import (
	"context"
	"errors"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog/v2"
)

const (
	conditionsPrefix      = "DefaultStorageClassController"
	infraConfigName       = "cluster"
	disabledConditionType = "Disabled"
)

var unsupportedPlatformError = errors.New("unsupported platform")
var supportedByCSIError = errors.New("only supported by a provided CSI Driver")

// This Controller deploys a default StorageClass for in-tree volume plugins,
// based on the underlying cloud (read from Infrastructure instance).
// It produces following Conditions:
// DefaultStorageClassControllerAvailable: the default storage class has been
//    created.
// DefaultStorageClassControllerProgressing - the default storage class has
//    not been created yet (typically on error).
// DefaultStorageClassControllerDegraded - error creating the storage class.
type Controller struct {
	operatorClient     v1helpers.OperatorClient
	kubeClient         kubernetes.Interface
	infraLister        openshiftv1.InfrastructureLister
	storageClassLister v1.StorageClassLister
	eventRecorder      events.Recorder
}

func NewController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder) factory.Controller {
	c := &Controller{
		operatorClient:     clients.OperatorClient,
		kubeClient:         clients.KubeClient,
		infraLister:        clients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		storageClassLister: clients.KubeInformers.InformersFor("").Storage().V1().StorageClasses().Lister(),
		eventRecorder:      eventRecorder,
	}
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.ConfigInformers.Config().V1().Infrastructures().Informer(),
		clients.KubeInformers.InformersFor("").Storage().V1().StorageClasses().Informer(),
	).ToController("DefaultStorageClassController", eventRecorder)
}

func (c *Controller) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("DefaultStorageClassController sync started")
	defer klog.V(4).Infof("DefaultStorageClassController sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	availableCnd := operatorapi.OperatorCondition{
		Type:   conditionsPrefix + operatorapi.OperatorStatusTypeAvailable,
		Status: operatorapi.ConditionTrue,
	}
	progressingCnd := operatorapi.OperatorCondition{
		Type:   conditionsPrefix + operatorapi.OperatorStatusTypeProgressing,
		Status: operatorapi.ConditionFalse,
	}

	syncErr := c.syncStorageClass(ctx)
	if syncErr != nil {
		if syncErr == unsupportedPlatformError {
			// Set Disabled condition - there is nothing to do
			disabledCnd := operatorapi.OperatorCondition{
				Type:    conditionsPrefix + disabledConditionType,
				Status:  operatorapi.ConditionTrue,
				Reason:  "UnsupportedPlatform",
				Message: syncErr.Error(),
			}
			// Set Available=true, Progressing=false - everything is OK and
			// there is nothing to do. ClusterOperatorStatusController needs
			// at least one Available/Pogressing condition set to mark the
			// overall ClusterOperator as Available + notPogressing.
			availableCnd.Message = "No default StorageClass for this platform"
			availableCnd.Status = operatorapi.ConditionTrue

			_, _, updateErr := v1helpers.UpdateStatus(context.TODO(), c.operatorClient,
				v1helpers.UpdateConditionFn(disabledCnd),
				v1helpers.UpdateConditionFn(availableCnd),
				v1helpers.UpdateConditionFn(progressingCnd),
			)
			return updateErr
		} else if syncErr == supportedByCSIError {
			// Set Available=true, Progressing=false - everything is OK
			// for this operator, but there may be work remaining for the
			// external CSI Drivers.
			availableCnd.Message = "StorageClass provided by supplied CSI Driver instead of the cluster-storage-operator"
			availableCnd.Status = operatorapi.ConditionTrue

			_, _, updateErr := v1helpers.UpdateStatus(context.TODO(), c.operatorClient,
				v1helpers.UpdateConditionFn(availableCnd),
				v1helpers.UpdateConditionFn(progressingCnd),
			)
			return updateErr
		}

		// Set Available=false, Progressing=true
		availableCnd.Status = operatorapi.ConditionFalse
		availableCnd.Reason = "SyncError"
		availableCnd.Message = syncErr.Error()
		progressingCnd.Status = operatorapi.ConditionTrue
		progressingCnd.Reason = "SyncError"
		progressingCnd.Message = syncErr.Error()
	}

	if _, _, updateErr := v1helpers.UpdateStatus(context.TODO(), c.operatorClient,
		v1helpers.UpdateConditionFn(availableCnd),
		v1helpers.UpdateConditionFn(progressingCnd),
		removeConditionFn(conditionsPrefix+disabledConditionType),
	); updateErr != nil {
		return errutil.NewAggregate([]error{syncErr, updateErr})
	}

	return syncErr
}

func (c *Controller) syncStorageClass(ctx context.Context) error {
	infrastructure, err := c.infraLister.Get(infraConfigName)
	if err != nil {
		return err
	}
	// Check to see if the PlatformStatus is nil. This has been seen on some
	// UPI installs on baremetal platforms
	if infrastructure.Status.PlatformStatus == nil {
		return unsupportedPlatformError
	}

	expectedSC, err := newStorageClassForCluster(infrastructure)
	if err != nil {
		return err
	}

	existingSC, err := c.storageClassLister.Get(expectedSC.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(2).Infof("StorageClass %s does not exist, creating", expectedSC.Name)
			_, _, err = resourceapply.ApplyStorageClass(ctx, c.kubeClient.StorageV1(), c.eventRecorder, expectedSC)
			return err
		}
		return err
	}

	// Don't overwrite default storage class annotations of the existing storage class!
	// User may have made it non-default.
	expectedSC.Annotations = existingSC.Annotations
	klog.V(2).Infof("Existing StorageClass %s found, reconciling", expectedSC.Name)
	_, _, err = resourceapply.ApplyStorageClass(ctx, c.kubeClient.StorageV1(), c.eventRecorder, expectedSC)

	return err
}

// Returns either the StorageClass, if the PlatformType is supported, or an error
// indicating whether the StorageClass is provided by a CSI driver or an unsupported platform
func newStorageClassForCluster(infrastructure *configv1.Infrastructure) (*storagev1.StorageClass, error) {
	var storageClassFile string
	switch infrastructure.Status.PlatformStatus.Type {
	case configv1.AWSPlatformType:
		storageClassFile = "storageclasses/aws.yaml"
	case configv1.AzurePlatformType:
		if infrastructure.Status.PlatformStatus.Azure != nil &&
			infrastructure.Status.PlatformStatus.Azure.CloudName == configv1.AzureStackCloud {
			return nil, supportedByCSIError
		}
		storageClassFile = "storageclasses/azure.yaml"
	case configv1.GCPPlatformType:
		storageClassFile = "storageclasses/gcp.yaml"
	case configv1.OpenStackPlatformType:
		storageClassFile = "storageclasses/openstack.yaml"
	case configv1.VSpherePlatformType:
		storageClassFile = "storageclasses/vsphere.yaml"
	case configv1.OvirtPlatformType:
		return nil, supportedByCSIError
	default:
		return nil, unsupportedPlatformError
	}

	scBytes, err := assets.ReadFile(storageClassFile)
	if err != nil {
		return nil, err
	}
	return resourceread.ReadStorageClassV1OrDie(scBytes), nil
}

// UpdateConditionFunc returns a func to update a condition.
func removeConditionFn(condType string) v1helpers.UpdateStatusFunc {
	return func(oldStatus *operatorapi.OperatorStatus) error {
		v1helpers.RemoveOperatorCondition(&oldStatus.Conditions, condType)
		return nil
	}
}
