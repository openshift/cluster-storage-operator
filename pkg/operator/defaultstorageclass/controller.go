package defaultstorageclass

import (
	"context"
	"errors"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
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

	syncErr := c.syncStorageClass()
	if syncErr != nil {
		if syncErr == unsupportedPlatformError {
			// Set Disabled condition - there is nothing to do
			disabledCnd := operatorapi.OperatorCondition{
				Type:    conditionsPrefix + disabledConditionType,
				Status:  operatorapi.ConditionTrue,
				Reason:  "UnsupportedPlatform",
				Message: syncErr.Error(),
			}
			_, _, updateErr := v1helpers.UpdateStatus(c.operatorClient,
				v1helpers.UpdateConditionFn(disabledCnd),
				removeConditionFn(conditionsPrefix+operatorapi.OperatorStatusTypeAvailable),
				removeConditionFn(conditionsPrefix+operatorapi.OperatorStatusTypeProgressing),
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

	if _, _, updateErr := v1helpers.UpdateStatus(c.operatorClient,
		v1helpers.UpdateConditionFn(availableCnd),
		v1helpers.UpdateConditionFn(progressingCnd),
		removeConditionFn(conditionsPrefix+disabledConditionType),
	); updateErr != nil {
		return errutil.NewAggregate([]error{syncErr, updateErr})
	}

	return syncErr
}

func (c *Controller) syncStorageClass() error {
	infrastructure, err := c.infraLister.Get(infraConfigName)
	if err != nil {
		return err
	}

	expectedSC, err := newStorageClassForCluster(infrastructure)
	if err != nil {
		return err
	}

	existingSC, err := c.storageClassLister.Get(expectedSC.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(2).Infof("StorageClass %s does not exist, creating", expectedSC.Name)
			_, _, err = resourceapply.ApplyStorageClass(c.kubeClient.StorageV1(), c.eventRecorder, expectedSC)
			return err
		}
		return err
	}

	// Don't overwrite default storage class annotations of the existing storage class!
	// User may have made it non-default.
	expectedSC.Annotations = existingSC.Annotations
	klog.V(2).Infof("Existing StorageClass %s found, reconciling", expectedSC.Name)
	_, _, err = resourceapply.ApplyStorageClass(c.kubeClient.StorageV1(), c.eventRecorder, expectedSC)

	return err
}

func newStorageClassForCluster(infrastructure *configv1.Infrastructure) (*storagev1.StorageClass, error) {
	switch infrastructure.Status.PlatformStatus.Type {
	case configv1.AWSPlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("storageclasses/aws.yaml")), nil
	case configv1.AzurePlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("storageclasses/azure.yaml")), nil
	case configv1.GCPPlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("storageclasses/gcp.yaml")), nil
	case configv1.OpenStackPlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("storageclasses/openstack.yaml")), nil
	case configv1.VSpherePlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("storageclasses/vsphere.yaml")), nil
	default:
		return nil, unsupportedPlatformError
	}
}

// UpdateConditionFunc returns a func to update a condition.
func removeConditionFn(condType string) v1helpers.UpdateStatusFunc {
	return func(oldStatus *operatorapi.OperatorStatus) error {
		v1helpers.RemoveOperatorCondition(&oldStatus.Conditions, condType)
		return nil
	}
}
