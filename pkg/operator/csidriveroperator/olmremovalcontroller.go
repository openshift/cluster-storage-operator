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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// This OLMOperatorRemovalController deletes pre-existing CSI driver installed by
// OLM to new CSI driver namespace.
// Steps performed:
//  1. Remove old OLM Subscription and CSV. Remember the operator namespace
//     (in Storage CR annotation), just in case the controller is restarted
//     after Subscription removal.
//  3. Remove the old CR (incl. force-removing all of its finalizers).
//
// It produces following conditions:
// <CSI driver name>OLMOperatorRemovalProgressing/Degraded: for status reporting.
// <CSI driver name>OLMOperatorRemovalAvailable: to signal that the removal has been complete
type OLMOperatorRemovalController struct {
	name           string
	operatorClient *operatorclient.OperatorClient
	olmOptions     *csioperatorclient.OLMOptions
	dynamicClient  dynamic.Interface
	kubeClient     kubernetes.Interface
	eventRecorder  events.Recorder
	factory        *factory.Factory

	olmOperatorNamespace string
	olmOperatorCSVName   string
}

const (
	olmOperatorRemovalControllerName = "OLMOperatorRemoval"

	// Annotation used to store OLM-based operator namespace in CSO's CR
	olmOperatorNamespaceAnnotation = ".olm-removal.storage.openshift.io/namespace"
	// Annotation used to store OLM-based operator CSV name in CSO's CR
	olmOperatorCSVAnnotation = ".olm-removal.storage.openshift.io/csvName"

	olmSource          = "redhat-operators"
	olmSourceNamespace = "openshift-marketplace"

	oldCRName = "cluster"

	// Interval used to check if an deleted objects was really removed from
	// API server. OLMOperatorRemovalController does not have informer on all
	// objects it needs to watch / remove (namely OLM CRDs and all Deployments
	// on the system, it would be too noisy).
	waitInterval = 5 * time.Second
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
	// Do *not* watch all Deployments in the cluster - that would be too noisy.
	f = f.WithInformers(clients.OperatorClient.Informer())

	c := &OLMOperatorRemovalController{
		name:           csiOperatorConfig.ConditionPrefix,
		operatorClient: clients.OperatorClient,
		olmOptions:     csiOperatorConfig.OLMOptions,
		dynamicClient:  clients.DynamicClient,
		kubeClient:     clients.KubeClient,
		eventRecorder:  eventRecorder.WithComponentSuffix(csiOperatorConfig.ConditionPrefix),
		factory:        f,
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
		c.olmOperatorCSVName = csvName
		// Delete the subscription, but remember the namespace of the operator
		// in CR's annotations first - just in case this controller is
		// restarted after it deletes the Subscription.
		if err := c.saveMetadata(subNamespace, csvName); err != nil {
			return err
		}

		removed, err := c.deleteSubscription(ctx, subNamespace, subName)
		if err != nil {
			return err
		}
		if !removed {
			klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for OLM Subscription to disappear")
			return c.markProgressing(ctx, syncCtx, "Waiting for OLM Subscription to be deleted")
		}
	}

	// In case no subscription exists, check that there is a saved one in the CSO CR annotations.
	if c.olmOperatorNamespace == "" {
		if c.olmOperatorNamespace, c.olmOperatorCSVName, err = c.loadMetadata(); err != nil {
			return err
		}
		klog.V(4).Infof("OLMOperatorRemovalController.Sync old namespace loaded: %q, csv: %q", c.olmOperatorNamespace, c.olmOperatorCSVName)
		if c.olmOperatorNamespace == "" {
			// There is no Subscription + there is no work recorded in CR annotations: we're done!
			klog.V(4).Infof("OLMOperatorRemovalController.Sync the old driver was not installed")
			// Since the old driver wasn't installed, we don't add any messages to avoid noisy Available/Progressing conditions.
			return c.markFinished(ctx, "")
		}
	}

	// 2. Delete CSV
	removed, err := c.deleteCSV(ctx, c.olmOperatorNamespace, c.olmOperatorCSVName)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for OLM CSV to disappear")
		return c.markProgressing(ctx, syncCtx, "Waiting for OLM CSV to be deleted")
	}

	// 3. Wait until OLM removes the the operator deployment
	removed, err = c.ensureOperatorDeploymentRemoved(ctx, c.olmOperatorNamespace, c.olmOptions.OLMOperatorDeploymentName)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for OLM to delete the operator")
		return c.markProgressing(ctx, syncCtx, "Waiting for OLM to delete the operator")
	}

	// 4. Remove CR
	removed, err = c.ensureCRRemoved(ctx, c.olmOptions.CRResource)
	if err != nil {
		return err
	}
	if !removed {
		klog.V(4).Infof("OLMOperatorRemovalController.Sync waiting for the CR to disappear")
		return c.markProgressing(ctx, syncCtx, "Waiting for the old operator CR to be deleted")
	}

	klog.V(4).Infof("OLMOperatorRemovalController.Sync done!")
	return c.markFinished(ctx, "CSI driver has been removed from OLM")
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

func (c *OLMOperatorRemovalController) saveMetadata(namespace string, csvName string) error {
	klog.V(4).Infof("Saving operator namespace annotation %q and CSV name %q", namespace, csvName)
	annotations := map[string]string{
		c.name + olmOperatorNamespaceAnnotation: namespace,
		c.name + olmOperatorCSVAnnotation:       csvName,
	}
	return c.operatorClient.SetObjectAnnotations(annotations)
}

func (c *OLMOperatorRemovalController) loadMetadata() (string, string, error) {
	meta, err := c.operatorClient.GetObjectMeta()
	if err != nil {
		return "", "", err
	}
	ns := meta.Annotations[c.name+olmOperatorNamespaceAnnotation]
	csv := meta.Annotations[c.name+olmOperatorCSVAnnotation]
	klog.V(4).Infof("Loaded operator namespace annotation %q, csv %q", ns, csv)
	return ns, csv, nil
}

func (c *OLMOperatorRemovalController) markProgressing(ctx context.Context, syncCtx factory.SyncContext, message string) error {
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

	if _, _, err := v1helpers.UpdateStatus(ctx, c.operatorClient,
		v1helpers.UpdateConditionFn(progressing),
		v1helpers.UpdateConditionFn(available),
	); err != nil {
		return err
	}

	// Re-sync after a while to check if there was any progress
	syncCtx.Queue().AddAfter(syncCtx.QueueKey(), waitInterval)

	return nil
}

func (c *OLMOperatorRemovalController) markFinished(ctx context.Context, message string) error {
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
	if err := c.saveMetadata("", ""); err != nil {
		return err
	}
	_, _, err := v1helpers.UpdateStatus(ctx, c.operatorClient,
		v1helpers.UpdateConditionFn(progressing),
		v1helpers.UpdateConditionFn(available),
	)
	return err
}

func (c *OLMOperatorRemovalController) ensureOperatorDeploymentRemoved(ctx context.Context, namespace, name string) (bool, error) {
	// Do not actively remove the Deployment here, that's OLM's job.
	_, err := c.kubeClient.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	klog.V(4).Infof("OLM Operator Deployment %s/%s still exists, waiting", namespace, name)
	// Don't report the Deployment is removed, wait until IsNotFound error above
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
	// Force remove finializers
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
