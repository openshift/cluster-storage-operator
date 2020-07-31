package csidriveroperator

import (
	"context"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

// This OLMOperatorRemovalController deletes pre-existing CSI driver installed by
// OLM to new CSI driver namespace.
// Steps performed:
// 1. Remove old OLM Subscription and CSV. Remember the operator namespace
//    (in Storage CR annotation), just in case the controller is restarted
//    after Subscription removal.
// 2. Remove the CSI driver Deployment and DaemonSet and wait until they're
//    really removed. Keep the old namespace around, as it can contain
//    Manila secrets that are used by existing PVs!
// 3. Remove the old CR (incl. removing of all of its finalizers).
// It produces following conditions:
// <CSI driver name>OLMOperatorRemovalProgressing/Degraded: for status reporting.
// <CSI driver name>OLMOperatorRemovalAvailable: to signal that the removal has been complete
type OLMOperatorRemovalController struct {
	name             string
	operatorClient   *operatorclient.OperatorClient
	olmOptions       *csioperatorclient.OLMOptions
	dynamicClient    dynamic.Interface
	kubeClient       kubernetes.Interface
	nsLister         corelisters.NamespaceLister
	deploymentLister appslisters.DeploymentLister
	daemonSetLister  appslisters.DaemonSetLister
	eventRecorder    events.Recorder
	factory          *factory.Factory

	olmOperatorNamespace string
}

const (
	olmOperatorRemovalControllerName = "OLMOperatorRemoval"

	olmOperatorNamespaceAnnotation = ".olm-removal.storage.openshift.io/namespace"
	olmSource                      = "redhat-operators"
	olmSourceNamespace             = "openshift-marketplace"

	oldCRName = "cluster"
)

func NewOLMOperatorRemovalController(
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	clients *csoclients.Clients,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
) *OLMOperatorRemovalController {

	if csiOperatorConfig.OLMOptions == nil {
		return nil
	}

	f := factory.New()
	f = f.ResyncEvery(resyncInterval)
	f = f.WithSyncDegradedOnError(clients.OperatorClient)
	// Necessary to do initial Sync after the controller starts.
	f = f.WithPostStartHooks(initalSync)
	// Add informers to the factory now, but the actual event handlers
	// are added later in CSIDriverOperatorCRController.Run(),
	// when we're 100% sure the controller is going to start (because it
	// depends on the platform).
	// If we added the event handlers now, all events would pile up in the
	// controller queue, without anything reading it.
	f = f.WithInformers(
		clients.OperatorClient.Informer(),
		// Watch Deployments in any namespace - CSO does not know where the OLM operator is installed
		clients.KubeInformers.InformersFor("").Apps().V1().Deployments().Informer(),
		clients.KubeInformers.InformersFor("").Apps().V1().DaemonSets().Informer())
	f = f.WithNamespaceInformer(
		clients.KubeInformers.InformersFor("").Core().V1().Namespaces().Informer(),
		csiOperatorConfig.OLMOptions.CSIDriverNamespace)

	c := &OLMOperatorRemovalController{
		name:             csiOperatorConfig.ConditionPrefix,
		operatorClient:   clients.OperatorClient,
		olmOptions:       csiOperatorConfig.OLMOptions,
		dynamicClient:    clients.DynamicClient,
		kubeClient:       clients.KubeClient,
		nsLister:         clients.KubeInformers.InformersFor("").Core().V1().Namespaces().Lister(),
		deploymentLister: clients.KubeInformers.InformersFor("").Apps().V1().Deployments().Lister(),
		daemonSetLister:  clients.KubeInformers.InformersFor("").Apps().V1().DaemonSets().Lister(),
		eventRecorder:    eventRecorder.WithComponentSuffix(csiOperatorConfig.ConditionPrefix),
		factory:          f,
	}
	return c
}

func (c *OLMOperatorRemovalController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("OLMOperatorRemovalController.Sync started")
	defer klog.V(4).Infof("OLMOperatorRemovalController.Sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	// 1. Find subscription + namespace
	subNamespace, subName, csvName, found, err := c.findSubscription(ctx)
	if err != nil {
		return err
	}

	if found {
		c.olmOperatorNamespace = subNamespace
		// Delete the subscription, but remember the namespace of the operator
		// in CR's annotations first - just in case this controller is
		// restarted after it deletes the Subscription.
		if err := c.saveOperatorNamespace(subNamespace); err != nil {
			return err
		}

		removed, err := c.deleteCSV(ctx, subNamespace, csvName)
		if err != nil {
			return err
		}
		if !removed {
			klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for OLM CSV to disappear")
			return c.markProgressing("Waiting for OLM CSV to be deleted")
		}

		removed, err = c.deleteSubscription(ctx, subNamespace, subName)
		if err != nil {
			return err
		}
		if !removed {
			klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for OLM Subscription to disappear")
			return c.markProgressing("Waiting for OLM Subscription to be deleted")
		}
	}

	// The subscription has been completely deleted...
	if c.olmOperatorNamespace == "" {
		// Load the namespace from annotation
		if c.olmOperatorNamespace, err = c.loadOperatorNamespace(); err != nil {
			return err
		}
		klog.V(4).Infof("OLMOperatorRemovalController.Sync old namespace loaded: %q", c.olmOperatorNamespace)
		if c.olmOperatorNamespace == "" {
			// There is no Subscription + there is no work recorded in CR annotations: we're done!
			klog.V(4).Infof("OLMOperatorRemovalController.Sync the old driver was not installed")
			return c.markFinished("CSI driver installed by OLM is not preset")
		}
	}

	// 3. Wait until OLM removes the the operator deployment
	removed, err := c.ensureOperatorDeploymentRemoved(c.olmOperatorNamespace, c.olmOptions.OLMOperatorDeploymentName)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for OLM to delete the operator")
		return c.markProgressing("Waiting for OLM to delete the operator")
	}

	// 4. Delete the driver Deployment. Note that the old operator pods can be still running at this point.
	removed, err = c.ensureDriverDeploymentRemoved(ctx, c.olmOptions.CSIDriverNamespace, c.olmOptions.CSIDriverDeploymentName)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("CSIDriverMigrationController.Sync waiting for the driver Deployment to disappear")
		return c.markProgressing("Waiting for OLM driver Deployment to be deleted")
	}

	// 5. Delete the driver DaemonSet.
	removed, err = c.ensureDriverDaemonSetRemoved(ctx, c.olmOptions.CSIDriverNamespace, c.olmOptions.CSIDriverDaemonSetName)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("CSIDriverMigrationController.Sync waiting for the driver DaemonSet to disappear")
		return c.markProgressing("Waiting for OLM driver DaemonSet to be deleted")
	}

	// 6. Remove CR
	removed, err = c.ensureCRRemoved(ctx, c.olmOptions.CRResource)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for the CR to disappear")
		return c.markProgressing("Waiting for the old operator CR to be deleted")
	}

	klog.V(4).Infof("OLMOperatorRemovalController.Sync done!")
	return c.markFinished("CSI driver has been removed from OLM")
}

func (c *OLMOperatorRemovalController) findSubscription(ctx context.Context) (string, string, string, bool, error) {
	subscriptions, err := c.dynamicClient.Resource(subscriptionResourceGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", "", "", false, err
	}
	for _, obj := range subscriptions.Items {
		pkg, found, err := unstructured.NestedString(obj.Object, "spec", "name")
		if !found {
			continue
		}
		if err != nil {
			return "", "", "", false, err
		}
		source, found, err := unstructured.NestedString(obj.Object, "spec", "source")
		if !found {
			continue
		}
		if err != nil {
			return "", "", "", false, err
		}
		sourceNamespace, found, err := unstructured.NestedString(obj.Object, "spec", "sourceNamespace")
		if !found {
			continue
		}
		if err != nil {
			return "", "", "", false, err
		}

		if pkg == c.olmOptions.OLMPackageName && source == olmSource && sourceNamespace == olmSourceNamespace {
			csvName, _, err := unstructured.NestedString(obj.Object, "status", "currentCSV")
			if err != nil {
				return "", "", "", false, err
			}
			klog.V(4).Infof("Found subscription %s/%s with CSV %s", obj.GetNamespace(), obj.GetName(), csvName)
			return obj.GetNamespace(), obj.GetName(), csvName, true, nil
		}
	}
	return "", "", "", false, nil
}

func (c *OLMOperatorRemovalController) deleteSubscription(ctx context.Context, namespace, name string) (bool, error) {
	err := c.dynamicClient.Resource(subscriptionResourceGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("Deleted subscription %s/%s", namespace, name)
	// Don't report the Subscription is removed, wait until IsNotFound error above
	return false, nil
}

func (c *OLMOperatorRemovalController) deleteCSV(ctx context.Context, namespace, name string) (bool, error) {
	err := c.dynamicClient.Resource(csvResourceGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("Deleted CSV %s/%s", namespace, name)
	// Don't report the CSV is removed, wait until IsNotFound error above
	return false, nil
}

func (c *OLMOperatorRemovalController) saveOperatorNamespace(namespace string) error {
	klog.V(4).Infof("Saved operator namespace annotation %s", namespace)
	return c.operatorClient.SetObjectAnnotation(c.name+olmOperatorNamespaceAnnotation, namespace)
}

func (c *OLMOperatorRemovalController) loadOperatorNamespace() (string, error) {
	meta, err := c.operatorClient.GetObjectMeta()
	if err != nil {
		return "", err
	}
	ns := meta.Annotations[c.name+olmOperatorNamespaceAnnotation]
	klog.V(4).Infof("Loaded operator namespace annotation %s", ns)
	return ns, nil
}

func (c *OLMOperatorRemovalController) markProgressing(message string) error {
	progressing := operatorapi.OperatorCondition{
		Type:    c.Name() + operatorapi.OperatorStatusTypeProgressing,
		Reason:  "RemovingOLMOperator",
		Status:  operatorapi.ConditionTrue,
		Message: message,
	}
	available := operatorapi.OperatorCondition{
		Type:    c.Name() + operatorapi.OperatorStatusTypeAvailable,
		Reason:  "RemovingOLMOperator",
		Status:  operatorapi.ConditionFalse,
		Message: message,
	}

	if _, _, err := v1helpers.UpdateStatus(c.operatorClient,
		v1helpers.UpdateConditionFn(progressing),
		v1helpers.UpdateConditionFn(available),
	); err != nil {
		return err
	}
	return nil
}

func (c *OLMOperatorRemovalController) markFinished(message string) error {
	progressing := operatorapi.OperatorCondition{
		Type:    c.Name() + operatorapi.OperatorStatusTypeProgressing,
		Reason:  "Finished",
		Status:  operatorapi.ConditionFalse,
		Message: message,
	}
	available := operatorapi.OperatorCondition{
		Type:    c.Name() + operatorapi.OperatorStatusTypeAvailable,
		Reason:  "Finished",
		Status:  operatorapi.ConditionTrue,
		Message: message,
	}

	// Clear the old namespace annotation - the driver has been fully removed.
	if err := c.saveOperatorNamespace(""); err != nil {
		return err
	}
	_, _, err := v1helpers.UpdateStatus(c.operatorClient,
		v1helpers.UpdateConditionFn(progressing),
		v1helpers.UpdateConditionFn(available),
	)
	return err
}

func (c *OLMOperatorRemovalController) ensureOperatorDeploymentRemoved(namespace, name string) (bool, error) {
	// Do not actively remove the Deployment here, that's OLM's job.
	_, err := c.deploymentLister.Deployments(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("Deleted Deployment %s/%s", namespace, name)
	// Don't report the Deployment is removed, wait until IsNotFound error above
	return false, nil
}

func (c *OLMOperatorRemovalController) ensureDriverDeploymentRemoved(ctx context.Context, namespace, name string) (bool, error) {
	_, err := c.deploymentLister.Deployments(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	err = c.kubeClient.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("Deleted driver Deployment %s/%s", namespace, name)
	// Don't report the Deployment is removed, wait until IsNotFound error above
	return false, nil
}

func (c *OLMOperatorRemovalController) ensureDriverDaemonSetRemoved(ctx context.Context, namespace, name string) (bool, error) {
	_, err := c.daemonSetLister.DaemonSets(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	err = c.kubeClient.AppsV1().DaemonSets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("Deleted driver DaemonSet %s/%s", namespace, name)
	// Don't report the DaemonSet is removed, wait until IsNotFound error above
	return false, nil
}

func (c *OLMOperatorRemovalController) ensureCRRemoved(ctx context.Context, res schema.GroupVersionResource) (bool, error) {
	cr, err := c.dynamicClient.Resource(res).Get(ctx, oldCRName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	if len(cr.GetFinalizers()) > 0 {
		cr.SetFinalizers([]string{})
		_, err = c.dynamicClient.Resource(res).Update(ctx, cr, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		klog.V(4).Infof("Deleted old CR finalizers")
	}
	err = c.dynamicClient.Resource(res).Delete(ctx, oldCRName, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("Deleted old CR")
	// Don't report the CR is removed, wait until IsNotFound error above
	return false, nil
}

func (c *OLMOperatorRemovalController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(c.name+csiDriverControllerName, c.eventRecorder)
	ctrl.Run(ctx, workers)
}

func (c *OLMOperatorRemovalController) Name() string {
	return c.name + olmOperatorRemovalControllerName
}

const (
	olmGroup             = "operators.coreos.com"
	olmVersion           = "v1alpha1"
	subscriptionResource = "subscriptions"
	csvResource          = "clusterserviceversions"
)

var subscriptionResourceGVR schema.GroupVersionResource = schema.GroupVersionResource{
	Group:    olmGroup,
	Version:  olmVersion,
	Resource: subscriptionResource,
}

var csvResourceGVR schema.GroupVersionResource = schema.GroupVersionResource{
	Group:    olmGroup,
	Version:  olmVersion,
	Resource: csvResource,
}

func olmRemovalComplete(cfg csioperatorclient.CSIOperatorConfig, operatorStatus *operatorapi.OperatorStatus) bool {
	if cfg.OLMOptions == nil {
		// This CSI driver does not need removal from OLM
		return true
	}
	return v1helpers.IsOperatorConditionTrue(
		operatorStatus.Conditions,
		cfg.ConditionPrefix+olmOperatorRemovalControllerName+operatorapi.OperatorStatusTypeAvailable)
}
