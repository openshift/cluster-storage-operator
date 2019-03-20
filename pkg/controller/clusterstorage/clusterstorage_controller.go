package clusterstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	installer "github.com/openshift/installer/pkg/types"
	v1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
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

const (
	// OwnerLabelNamespace is the label key for the owner namespace
	OwnerLabelNamespace = "cluster.storage.openshift.io/owner-namespace"
	// OwnerLabelName is the label key for the owner name
	OwnerLabelName = "cluster.storage.openshift.io/owner-name"
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

	// Watch for changes to primary resource ConfigMap
	// We treat cluster-config-v1 as the primary resource because it says what the
	// cluster cloud provider is
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isClusterConfig(e.Meta) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isClusterConfig(e.Meta) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isClusterConfig(e.MetaNew) },
		GenericFunc: func(e event.GenericEvent) bool { return isClusterConfig(e.Meta) },
	})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource StorageClasses and requeue the owner ConfigMap
	err = c.Watch(&source.Kind{Type: &storagev1.StorageClass{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(a handler.MapObject) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Namespace: a.Meta.GetLabels()[OwnerLabelNamespace],
					Name:      a.Meta.GetLabels()[OwnerLabelName],
				}},
			}
		}),
	})
	if err != nil {
		return err
	}

	return nil
}

func isClusterConfig(meta metav1.Object) bool {
	return meta.GetNamespace() == "kube-system" && meta.GetName() == "cluster-config-v1"
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
	reqLogger.Info("Reconciling ConfigMap")

	// Fetch the ConfigMap instance
	instance := &corev1.ConfigMap{}
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
			Name:      "storage",
			Namespace: corev1.NamespaceAll,
		},
	}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: "storage", Namespace: corev1.NamespaceAll}, clusterOperatorInstance)
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

	err = r.setStatusProgressing(clusterOperatorInstance)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Define a new StorageClass object
	sc, err := newStorageClassForCluster(instance)
	if err != nil {
		_ = r.syncStatus(clusterOperatorInstance, err)
		// requeue only if platform is supported
		if err != unsupportedPlatformError {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	// Set ConfigMap instance as the owner and controller
	sc.SetLabels(labelsForClusterStorage(instance))

	// Check if this StorageClass already exists
	found := &storagev1.StorageClass{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: sc.Name, Namespace: corev1.NamespaceAll}, found)
	if err != nil && apierrors.IsNotFound(err) {
		reqLogger.Info("Creating a new StorageClass", "StorageClass.Name", sc.Name)
		err = r.client.Create(context.TODO(), sc)
		if err != nil {
			_ = r.syncStatus(clusterOperatorInstance, err)
			return reconcile.Result{}, err
		}

		// StorageClass created successfully - don't requeue
		_ = r.syncStatus(clusterOperatorInstance, nil)
		return reconcile.Result{}, nil
	} else if err != nil {
		_ = r.syncStatus(clusterOperatorInstance, err)
		return reconcile.Result{}, err
	}

	// StorageClass already exists - don't requeue
	reqLogger.Info("Skip reconcile: StorageClass already exists", "StorageClass.Name", found.Name)
	_ = r.syncStatus(clusterOperatorInstance, nil)
	return reconcile.Result{}, nil
}

var (
	unavailable = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorAvailable,
		Status: configv1.ConditionFalse,
	}
	notFailing = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorFailing,
		Status: configv1.ConditionFalse,
	}
	notProgressing = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorProgressing,
		Status: configv1.ConditionFalse,
	}
)

// setStatusProgressing sets Available=false;Failing=false;Progressing=true
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
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notFailing)

	progressing := configv1.ClusterOperatorStatusCondition{
		Type:   configv1.OperatorProgressing,
		Status: configv1.ConditionTrue,
	}
	if len(releaseVersion) > 0 {
		progressing.Message = fmt.Sprintf("Working towards %v", releaseVersion)
	}
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, progressing)

	updateErr := r.client.Status().Update(context.TODO(), clusterOperator)
	if updateErr != nil {
		log.Error(updateErr, "Failed to update ClusterOperator status")
		return updateErr
	}
	return nil
}

// syncStatus will set either Available=true;Failing=false;Progressing=false
// or Available=false;Failing=true;Progressing=false depending on the error
func (r *ReconcileClusterStorage) syncStatus(clusterOperator *configv1.ClusterOperator, err error) error {
	// we set versions if we are "available" to indicate we have rolled out the latest
	// version of the cluster storage object
	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		if err == nil {
			clusterOperator.Status.Versions = []configv1.OperandVersion{{Name: "operator", Version: releaseVersion}}
		}
	} else {
		clusterOperator.Status.Versions = nil
	}

	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notProgressing)

	var message string

	// if error is anything other than unsupported platform, we are failing
	if err != nil {
		if err != unsupportedPlatformError {
			failing := configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "Error",
				Message: err.Error(),
			}
			v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, failing)
			v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, unavailable)

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
		Status: configv1.ConditionTrue,
	}
	if message != "" {
		available.Message = message
	}
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, available)
	v1helpers.SetStatusCondition(&clusterOperator.Status.Conditions, notFailing)

	updateErr := r.client.Status().Update(context.TODO(), clusterOperator)
	if updateErr != nil {
		log.Error(updateErr, "Failed to update ClusterOperator status")
		return updateErr
	}
	return nil
}

func newStorageClassForCluster(cm *corev1.ConfigMap) (*storagev1.StorageClass, error) {
	platform, err := getPlatform(cm)
	if err != nil {
		return nil, err
	}

	if platform.AWS != nil {
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/aws.yaml")), nil
	} else if platform.OpenStack != nil {
		return resourceread.ReadStorageClassV1OrDie(generated.MustAsset("assets/openstack.yaml")), nil
	}

	return nil, unsupportedPlatformError
}

func getPlatform(cm *corev1.ConfigMap) (*installer.Platform, error) {
	data, err := utilyaml.ToJSON([]byte(cm.Data["install-config"]))
	if err != nil {
		return nil, err
	}

	config := &installer.InstallConfig{}
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config.Platform, nil
}

// labelsForClusterStorage returns the labels for selecting the resources
// belonging to the given cluster storage config
func labelsForClusterStorage(cm *corev1.ConfigMap) map[string]string {
	return map[string]string{
		OwnerLabelNamespace: cm.Namespace,
		OwnerLabelName:      cm.Name,
	}
}
