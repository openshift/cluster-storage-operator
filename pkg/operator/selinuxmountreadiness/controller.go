package selinuxmountreadiness

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

const (
	conditionsPrefix = "SELinuxMountGAReadinessController"


	selinuxConflictsConfigMapName = "selinux-conflicts"
	selinuxConflictsDataKey       = "conflictsPresent"

	// SELinuxMountGAReadinessFeatureGate gates the upgrade readiness check until
	// openshift/api exposes features.FeatureGateSELinuxMountGAReadiness.
	SELinuxMountGAReadinessFeatureGate = configv1.FeatureGateName("SELinuxMountGAReadiness")

	// KCSArticleURL is linked from Prometheus alerts. Update when the KCS article is published.
	KCSArticleURL = "https://github.com/openshift/enhancements/blob/master/enhancements/storage/selinuxmount-ga-block-upgrade.md"
)

// Controller watches openshift-config/selinux-conflicts written by the
// SELinuxWarningController in kube-controller-manager and sets the storage
// operator Upgradeable condition accordingly.
type Controller struct {
	operatorClient v1helpers.OperatorClient
	configMapLister corelisters.ConfigMapNamespaceLister
	featureGates   featuregates.FeatureGate
	eventRecorder  events.Recorder
}

func NewController(
	clients *csoclients.Clients,
	featureGates featuregates.FeatureGate,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &Controller{
		operatorClient: clients.OperatorClient,
		configMapLister: clients.KubeInformers.
			InformersFor(csoclients.CloudConfigNamespace).
			Core().V1().ConfigMaps().Lister().ConfigMaps(csoclients.CloudConfigNamespace),
		featureGates:  featureGates,
		eventRecorder: eventRecorder,
	}
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.KubeInformers.InformersFor(csoclients.CloudConfigNamespace).Core().V1().ConfigMaps().Informer(),
		clients.ConfigInformers.Config().V1().FeatureGates().Informer(),
	).ToController("SELinuxMountGAReadinessController", eventRecorder)
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

	if !c.isFeatureGateEnabled() {
		_, _, err := v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(upgradeableCnd))
		return err
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

func (c *Controller) isFeatureGateEnabled() bool {
	if c.featureGates == nil {
		return false
	}
	for _, known := range c.featureGates.KnownFeatures() {
		if known == SELinuxMountGAReadinessFeatureGate {
			return c.featureGates.Enabled(SELinuxMountGAReadinessFeatureGate)
		}
	}
	return false
}

func (c *Controller) conflictsPresent() (present bool, found bool, err error) {
	cm, err := c.configMapLister.Get(selinuxConflictsConfigMapName)
	if apierrors.IsNotFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	value, ok := cm.Data[selinuxConflictsDataKey]
	if !ok {
		klog.V(2).Infof("ConfigMap %s/%s exists but is missing key %q, treating as no conflicts",
			csoclients.CloudConfigNamespace, selinuxConflictsConfigMapName, selinuxConflictsDataKey)
		return false, true, nil
	}
	return metav1.ConditionStatus(value) == metav1.ConditionTrue, true, nil
}

func upgradeBlockedMessage() string {
	return fmt.Sprintf(
		"Workloads incompatible with SELinuxMount GA were detected and could break after upgrade to the next release. "+
			"See metric selinux_warning_controller_selinux_volume_conflict to list all affected pods. "+
			"See %s for remediation.",
		KCSArticleURL,
	)
}

// ConflictsPresent reports whether the selinux-conflicts ConfigMap indicates conflicts.
// It is exported for unit tests.
func ConflictsPresent(cm *corev1.ConfigMap) (present bool, found bool) {
	if cm == nil {
		return false, false
	}
	value, ok := cm.Data[selinuxConflictsDataKey]
	if !ok {
		return false, true
	}
	return metav1.ConditionStatus(value) == metav1.ConditionTrue, true
}
