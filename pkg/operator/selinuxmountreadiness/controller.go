package selinuxmountreadiness

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	// features "github.com/openshift/api/features" // TODO(openshift/api#2882): uncomment after openshift/api vendor bump.
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	conditionsPrefix = "SELinuxMountGAReadinessController"

	selinuxConflictsConfigMapName = "selinux-conflicts"
	selinuxConflictsDataKey       = "conflictsPresent"

	configMapInformerResync = 10 * time.Minute

	// TODO(openshift/api#2882): delete this local constant and use features.FeatureGateSELinuxMountGAReadiness.
	SELinuxMountGAReadinessFeatureGate = configv1.FeatureGateName("SELinuxMountGAReadiness")
	// SELinuxMountGAReadinessFeatureGate = features.FeatureGateSELinuxMountGAReadiness

	// KCSArticleURL is linked from Prometheus alerts. Update when the KCS article is published.
	KCSArticleURL = "https://github.com/openshift/enhancements/blob/master/enhancements/storage/selinuxmount-ga-block-upgrade.md"
)

// FeatureGateEnabled reports whether SELinuxMountGAReadiness is a known and enabled feature gate.
func FeatureGateEnabled(fg featuregates.FeatureGate) bool {
	if fg == nil {
		return false
	}
	for _, known := range fg.KnownFeatures() {
		if known == SELinuxMountGAReadinessFeatureGate {
			return fg.Enabled(SELinuxMountGAReadinessFeatureGate)
		}
	}
	return false
}

// Controller watches openshift-config/selinux-conflicts written by the
// SELinuxWarningController in kube-controller-manager and sets the storage
// operator Upgradeable condition accordingly.
type Controller struct {
	operatorClient  v1helpers.OperatorClient
	configMapLister corelisters.ConfigMapNamespaceLister
	eventRecorder   events.Recorder
}

func NewController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder,
) (factory.Controller, cache.SharedIndexInformer) {
	configMapInformer := newSELinuxConflictsConfigMapInformer(clients.KubeClient)

	c := &Controller{
		operatorClient: clients.OperatorClient,
		configMapLister: corelisters.NewConfigMapLister(configMapInformer.GetIndexer()).
			ConfigMaps(csoclients.CloudConfigNamespace),
		eventRecorder: eventRecorder,
	}
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		configMapInformer,
	).ToController("SELinuxMountGAReadinessController", eventRecorder), configMapInformer
}

// RunConfigMapInformer starts the dedicated ConfigMap informer returned by NewController.
// Call it before the controller Run loop, alongside csoclients.StartInformers.
func RunConfigMapInformer(informer cache.SharedIndexInformer, stopCh <-chan struct{}) {
	go informer.Run(stopCh)
}

func newSELinuxConflictsConfigMapInformer(kubeClient kubernetes.Interface) cache.SharedIndexInformer {
	return coreinformers.NewFilteredConfigMapInformer(
		kubeClient,
		csoclients.CloudConfigNamespace,
		configMapInformerResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		func(listOptions *metav1.ListOptions) {
			listOptions.FieldSelector = fields.OneTermEqualSelector("metadata.name", selinuxConflictsConfigMapName).String()
		},
	)
}

func (c *Controller) sync(ctx context.Context, _ factory.SyncContext) error {
	klog.V(4).Info("SELinuxMountGAReadinessController sync started")
	defer klog.V(4).Info("SELinuxMountGAReadinessController sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	upgradeableCnd := operatorapi.OperatorCondition{
		Type:   conditionsPrefix + operatorapi.OperatorStatusTypeUpgradeable,
		Status: operatorapi.ConditionTrue,
	}

	conflictsPresent, found, err := c.conflictsPresent()
	if err != nil {
		return err
	}
	if found && conflictsPresent {
		upgradeableCnd.Status = operatorapi.ConditionFalse
		upgradeableCnd.Reason = "SELinuxMountIncompatibleWorkloads"
		upgradeableCnd.Message = upgradeBlockedMessage()
	}

	_, _, err = v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(upgradeableCnd))
	return err
}

func (c *Controller) conflictsPresent() (present bool, found bool, err error) {
	cm, err := c.configMapLister.Get(selinuxConflictsConfigMapName)
	if apierrors.IsNotFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	present, found = conflictsPresentInConfigMap(cm)
	return present, found, nil
}

func upgradeBlockedMessage() string {
	return fmt.Sprintf(
		"Workloads incompatible with SELinuxMount GA were detected and could break after upgrade to the next release. "+
			"See metric selinux_warning_controller_selinux_volume_conflict to list all affected pods. "+
			"See %s for remediation.",
		KCSArticleURL,
	)
}

// conflictsPresentInConfigMap reports whether the selinux-conflicts ConfigMap data indicates conflicts.
func conflictsPresentInConfigMap(cm *corev1.ConfigMap) (present bool, found bool) {
	if cm == nil {
		return false, false
	}
	value, ok := cm.Data[selinuxConflictsDataKey]
	if !ok {
		klog.V(2).Infof("ConfigMap %s/%s exists but is missing key %q, treating as no conflicts",
			csoclients.CloudConfigNamespace, selinuxConflictsConfigMapName, selinuxConflictsDataKey)
		return false, true
	}
	return metav1.ConditionStatus(value) == metav1.ConditionTrue, true
}
