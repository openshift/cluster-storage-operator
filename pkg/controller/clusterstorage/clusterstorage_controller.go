package clusterstorage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/go-logr/logr"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	v1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	ocontroller "github.com/openshift/library-go/pkg/controller"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_clusterstorage")
var unsupportedPlatformError = errors.New("unsupported platform")
var oldNamespaceExistsError = errors.New("old namespace still exists")

const (
	infrastructureName          = "cluster"
	clusterOperatorName         = "storage"
	oldClusterOperatorNamespace = "openshift-cluster-storage-operator"
)

// Add creates a new ClusterStorage Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileClusterStorage{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("clusterstorage-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Infrastructure
	err = c.Watch(&source.Kind{Type: &configv1.Infrastructure{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return e.Meta.GetName() == infrastructureName },
		DeleteFunc:  func(e event.DeleteEvent) bool { return e.Meta.GetName() == infrastructureName },
		UpdateFunc:  func(e event.UpdateEvent) bool { return e.MetaNew.GetName() == infrastructureName },
		GenericFunc: func(e event.GenericEvent) bool { return e.Meta.GetName() == infrastructureName },
	})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource StorageClasses and requeue the Infrastructure
	err = c.Watch(&source.Kind{Type: &storagev1.StorageClass{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(a handler.MapObject) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Namespace: corev1.NamespaceAll,
					Name:      infrastructureName,
				}},
			}
		}),
	})
	if err != nil {
		return err
	}

	// Upgrade OCP 4.4 -> 4.5: watch the old namespace and delete it.
	// TODO: remove in 4.6
	source := &source.Kind{Type: &corev1.Namespace{}}
	handler := &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(a handler.MapObject) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Namespace: corev1.NamespaceAll,
					Name:      infrastructureName,
				}},
			}
		}),
	}
	predicate := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return e.Meta.GetName() == oldClusterOperatorNamespace },
		DeleteFunc:  func(e event.DeleteEvent) bool { return e.Meta.GetName() == oldClusterOperatorNamespace },
		UpdateFunc:  func(e event.UpdateEvent) bool { return e.MetaNew.GetName() == oldClusterOperatorNamespace },
		GenericFunc: func(e event.GenericEvent) bool { return e.Meta.GetName() == oldClusterOperatorNamespace },
	}
	err = c.Watch(source, handler, predicate)

	return nil
}

var _ reconcile.Reconciler = &ReconcileClusterStorage{}

// ReconcileClusterStorage reconciles a ClusterStorage object
type ReconcileClusterStorage struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a ClusterStorage object and makes changes based on the state read
// and what is in the ClusterStorage.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileClusterStorage) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Infrastructure")

	// Fetch the Infrastructure instance
	instance := &configv1.Infrastructure{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	clusterOperatorInstance := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterOperatorName,
			Namespace: corev1.NamespaceAll,
		},
	}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: clusterOperatorName, Namespace: corev1.NamespaceAll}, clusterOperatorInstance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Must create and update it because CVO waits for it
			err = r.client.Create(context.TODO(), clusterOperatorInstance)
			if err != nil {
				return reconcile.Result{}, err
			}
			// Continue
		} else {
			// Error reading the object - requeue the request.
			return reconcile.Result{}, err
		}
	}

	clusterOperatorInstance.Status.RelatedObjects = getRelatedObjects(nil)
	err = r.setStatusProgressing(clusterOperatorInstance)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Make sure the default storage class exists
	sc, storageClassErr := r.ensureDefaultStorageClass(instance, reqLogger)
	clusterOperatorInstance.Status.RelatedObjects = getRelatedObjects(sc)

	// Delete old namespace
	namespaceDeletionErr := r.deleteOldNamespace(reqLogger)
	r.syncStatus(clusterOperatorInstance, storageClassErr, namespaceDeletionErr)

	if namespaceDeletionErr == oldNamespaceExistsError {
		// No need to requeue, the controller gets Namespace event when the namespace is deleted.
		// Returning namespaceDeletionErr would just pollute logs.
		namespaceDeletionErr = nil
	}

	if storageClassErr != nil {
		return reconcile.Result{}, storageClassErr
	}
	return reconcile.Result{}, namespaceDeletionErr
}

func (r *ReconcileClusterStorage) ensureDefaultStorageClass(instance *configv1.Infrastructure, logger logr.Logger) (*storagev1.StorageClass, error) {
	// Define a new StorageClass object
	newSCFromFile, err := newStorageClassForCluster(instance)
	if err != nil {
		// requeue only if platform is supported
		if err != unsupportedPlatformError {
			return nil, err
		}
		return nil, nil
	}

	// Set the clusteroperator to be the owner of the SC
	ocontroller.EnsureOwnerRef(newSCFromFile, metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "clusteroperator",
		Name:       clusterOperatorName,
		UID:        instance.GetUID(),
	})

	// Check if this StorageClass already exists
	existingSC := &storagev1.StorageClass{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: newSCFromFile.Name, Namespace: corev1.NamespaceAll}, existingSC)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new StorageClass", "StorageClass.Name", newSCFromFile.Name)
		err = r.client.Create(context.TODO(), newSCFromFile)
		return newSCFromFile, err
	} else if err != nil {
		return newSCFromFile, err
	}

	// Check to see if modifications have been made to the StorageClass attributes
	comparisonSC := newSCFromFile.DeepCopy()
	// Copy over the ObjectMeta, which includes the annotations and labels
	comparisonSC.ObjectMeta = existingSC.ObjectMeta

	// Define a default ReclaimPolicy for comparison
	if comparisonSC.ReclaimPolicy == nil {
		deletePolicy := corev1.PersistentVolumeReclaimDelete
		comparisonSC.ReclaimPolicy = &deletePolicy
	}

	// If a change has been detected, update the StorageClass.
	if !reflect.DeepEqual(comparisonSC, existingSC) {
		logger.Info("StorageClass already exists and needs to be updated", "StorageClass.Name", existingSC.Name)

		// Restore original delete policy (for stable unit tests)
		comparisonSC.ReclaimPolicy = newSCFromFile.ReclaimPolicy

		err = r.client.Update(context.TODO(), comparisonSC)
		return newSCFromFile, err
	}

	// StorageClass already exists and doesn't need to be updated - don't requeue
	logger.Info("Skip reconcile: StorageClass already exists", "StorageClass.Name", existingSC.Name)
	return newSCFromFile, nil
}

func (r *ReconcileClusterStorage) deleteOldNamespace(logger logr.Logger) error {
	ns := &corev1.Namespace{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: oldClusterOperatorNamespace, Namespace: corev1.NamespaceAll}, ns)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Nothing to delete
			return nil
		}
		logger.Error(err, "Failed to get namespace "+oldClusterOperatorNamespace)
		return err
	}

	// Old namespace found
	if err = r.client.Delete(context.TODO(), ns); err != nil {
		logger.Error(err, "Failed to delete namespace "+oldClusterOperatorNamespace)
		return err
	}
	logger.Info("Deleted namespace " + oldClusterOperatorNamespace)
	// Report error, so the progressing condition is true.
	// It will be cleared when the namespace is not found (few lines above, on the next resync).
	return oldNamespaceExistsError
}

var (
	unavailable = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorAvailable,
		Reason: "AsExpected",
		Status: configv1.ConditionFalse,
	}
	notDegraded = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorDegraded,
		Reason: "AsExpected",
		Status: configv1.ConditionFalse,
	}
	notProgressing = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorProgressing,
		Reason: "AsExpected",
		Status: configv1.ConditionFalse,
	}
	notUpgradeable = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorUpgradeable,
		Reason: "AsExpected",
		Status: configv1.ConditionFalse,
	}
)

// setStatusProgressing sets Available=false;Degraded=false;Progressing=true;Upgradeable=false
// we set "progressing" if the cluster operator's version is not the latest
// and we are about to try to roll it out
func (r *ReconcileClusterStorage) setStatusProgressing(clusterOperator *configv1.ClusterOperator) error {
	releaseVersion := os.Getenv("RELEASE_VERSION")
	if len(releaseVersion) > 0 {
		for _, version := range clusterOperator.Status.Versions {
			if version.Name == "operator" && version.Version == releaseVersion {
				// release version matches, do nothing
				return nil
			}
		}
	}
	// release version is nil or doesn't match, we will try to roll out the
	// latest so set progressing=true

	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, unavailable)
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notDegraded)
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notUpgradeable)

	progressing := configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorProgressing,
		Reason: "AsExpected",
		Status: configv1.ConditionTrue,
	}
	if len(releaseVersion) > 0 {
		progressing.Message = fmt.Sprintf("Working towards %v", releaseVersion)
	}
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, progressing)

	clusterOperator.Status.RelatedObjects = getRelatedObjects(nil)

	updateErr := r.client.Status().Update(context.TODO(), clusterOperator)
	if updateErr != nil {
		log.Error(updateErr, "Failed to update ClusterOperator status")
		return updateErr
	}
	return nil
}

// syncStatus will set either Available=true;Degraded=false;Progressing=false;Upgradeable=true
// or Available=false;Degraded=true;Progressing=false;Upgradeable=false depending on the error
func (r *ReconcileClusterStorage) syncStatus(clusterOperator *configv1.ClusterOperator, storageClassErr, namespaceDeletionErr error) error {
	// we set versions if we are "available" to indicate we have rolled out the latest
	// version of the cluster storage object
	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		if storageClassErr == nil || storageClassErr == unsupportedPlatformError {
			clusterOperator.Status.Versions = []configv1.OperandVersion{{Name: "operator", Version: releaseVersion}}
		}
	} else {
		clusterOperator.Status.Versions = nil
	}

	var message string

	// if error is anything other than unsupported platform, we are degraded
	if storageClassErr != nil {
		if storageClassErr != unsupportedPlatformError {
			degraded := configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorDegraded,
				Status:  configv1.ConditionTrue,
				Reason:  "Error",
				Message: storageClassErr.Error(),
			}
			v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, degraded)
			v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, unavailable)
			v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notUpgradeable)

			updateErr := r.client.Status().Update(context.TODO(), clusterOperator)
			if updateErr != nil {
				log.Error(updateErr, "Failed to update ClusterOperator status")
				return updateErr
			}
			return nil
		}
		message = "Unsupported platform for storageclass creation"
	}

	available := configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorAvailable,
		Reason: "AsExpected",
		Status: configv1.ConditionTrue,
	}
	if message != "" {
		available.Message = message
	}

	upgradeable := configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorUpgradeable,
		Reason: "AsExpected",
		Status: configv1.ConditionTrue,
	}

	progressing := notProgressing
	if namespaceDeletionErr != nil {
		// Waiting for namespace deletion.
		progressing.Status = configv1.ConditionTrue
		progressing.Message = "Deleting old namespace"
	}

	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, progressing)
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, available)
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notDegraded)
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, upgradeable)

	updateErr := r.client.Status().Update(context.TODO(), clusterOperator)
	if updateErr != nil {
		log.Error(updateErr, "Failed to update ClusterOperator status")
		return updateErr
	}
	return nil
}

func newStorageClassForCluster(infrastructure *configv1.Infrastructure) (*storagev1.StorageClass, error) {
	switch infrastructure.Status.Platform {
	case configv1.AWSPlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/aws.yaml")), nil
	case configv1.AzurePlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/azure.yaml")), nil
	case configv1.GCPPlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/gcp.yaml")), nil
	case configv1.OpenStackPlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/openstack.yaml")), nil
	case configv1.VSpherePlatformType:
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/vsphere.yaml")), nil
	default:
		return nil, unsupportedPlatformError
	}
}

func getRelatedObjects(sc *storagev1.StorageClass) []configv1.ObjectReference {
	relatedObjects := []configv1.ObjectReference{
		{Resource: "namespaces", Name: "openshift-cluster-storage-operators"},
		{Group: "config.openshift.io", Resource: "infrastructures", Name: infrastructureName},
	}
	if sc != nil {
		obj := configv1.ObjectReference{
			Group:    "storage.k8s.io",
			Resource: "storageclasses",
			Name:     sc.Name}
		relatedObjects = append(relatedObjects, obj)
	}
	return relatedObjects
}
