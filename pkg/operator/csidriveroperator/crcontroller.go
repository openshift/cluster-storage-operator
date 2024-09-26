package csidriveroperator

import (
	"context"
	"fmt"
	"strings"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	oplisters "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// This CSIDriverOperatorCRController installs and syncs CSI driver operator CR. It monitors the
// CR status and merges all its conditions to the CSO CR.
// It produces following Conditions:
// <CSI driver name>CSIDriverOperatorDegraded on error
// <CSI driver name>CSIDriverOperatorCRDegraded - copied from *Degraded conditions from CR.
// <CSI driver name>CSIDriverOperatorCRAvailable - copied from *Available conditions from CR.
// <CSI driver name>CSIDriverOperatorCRProgressing - copied from *Progressing conditions from CR.
type CSIDriverOperatorCRController struct {
	name                   string
	operatorClient         v1helpers.OperatorClient
	kubeClient             kubernetes.Interface
	operatorClientSet      opclient.Interface
	clusterCSIDriverLister oplisters.ClusterCSIDriverLister
	eventRecorder          events.Recorder
	factory                *factory.Factory
	csiDriverName          string
	csiDriverAsset         string
	allowDisabled          bool
}

var _ factory.Controller = &CSIDriverOperatorCRController{}

const (
	csiDriverControllerName            = "CSIDriverOperator"
	csiDriverControllerConditionPrefix = "CSIDriverOperatorCR"
	versionName                        = "CSIDriverOperator"
)

var (
	opScheme = runtime.NewScheme()
	opCodecs = serializer.NewCodecFactory(opScheme)
)

func init() {
	if err := operatorapi.AddToScheme(opScheme); err != nil {
		panic(err)
	}
}

func NewCSIDriverOperatorCRController(
	name string,
	clients *csoclients.Clients,
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
) factory.Controller {
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
		clients.OperatorInformers.Operator().V1().ClusterCSIDrivers().Informer(),
		clients.KubeInformers.InformersFor(csoclients.CSIOperatorNamespace).Apps().V1().Deployments().Informer())

	c := &CSIDriverOperatorCRController{
		name:                   name,
		operatorClient:         clients.OperatorClient,
		kubeClient:             clients.KubeClient,
		operatorClientSet:      clients.OperatorClientSet,
		clusterCSIDriverLister: clients.OperatorInformers.Operator().V1().ClusterCSIDrivers().Lister(),
		eventRecorder:          eventRecorder.WithComponentSuffix(name),
		factory:                f,
		csiDriverName:          csiOperatorConfig.CSIDriverName,
		csiDriverAsset:         csiOperatorConfig.CRAsset,
		allowDisabled:          csiOperatorConfig.AllowDisabled,
	}
	return c
}

func (c *CSIDriverOperatorCRController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverOperatorCRController sync started")
	defer klog.V(4).Infof("CSIDriverOperatorCRController sync finished")

	var errs []error
	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	// Sync CSIDriver CR
	requiredCR := c.getRequestedClusterCSIDriver(opSpec.LogLevel)
	cr, _, err := c.applyClusterCSIDriver(ctx, requiredCR)
	if err != nil {
		// This will set Degraded condition
		return err
	}

	newGeneration := applyoperatorv1.GenerationStatus().
		WithGroup(operatorapi.GroupName).
		WithResource("clustercsidrivers").
		WithNamespace(cr.Namespace).
		WithName(cr.Name).
		WithLastGeneration(cr.ObjectMeta.Generation)

	if err := c.syncConditions(ctx, cr.Status.Conditions, newGeneration); err != nil {
		errs = append(errs, err)
	}
	return errors.NewAggregate(errs)
}

func (c *CSIDriverOperatorCRController) getRequestedClusterCSIDriver(logLevel operatorapi.LogLevel) *operatorapi.ClusterCSIDriver {
	if logLevel == "" {
		logLevel = operatorapi.Normal
	}
	assetBytes, err := assets.ReadFile(c.csiDriverAsset)
	if err != nil {
		panic(err)
	}
	cr := readClusterCSIDriverOrDie(assetBytes)
	cr.Spec.LogLevel = logLevel
	cr.Spec.OperatorLogLevel = logLevel
	cr.Spec.ManagementState = operatorapi.Managed
	return cr
}

func (c *CSIDriverOperatorCRController) Run(ctx context.Context, workers int) {
	// This adds event handlers to informers.
	ctrl := c.factory.WithSync(c.Sync).ToController(c.Name(), c.eventRecorder)
	ctrl.Run(ctx, workers)
}

func (c *CSIDriverOperatorCRController) Name() string {
	return c.name + csiDriverControllerName
}

func (c *CSIDriverOperatorCRController) crConditionName(cndType string) string {
	return c.name + csiDriverControllerConditionPrefix + cndType
}

func (c *CSIDriverOperatorCRController) applyClusterCSIDriver(ctx context.Context, required *operatorapi.ClusterCSIDriver) (*operatorapi.ClusterCSIDriver, bool, error) {
	existing, err := c.operatorClientSet.OperatorV1().ClusterCSIDrivers().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := c.operatorClientSet.OperatorV1().ClusterCSIDrivers().Create(ctx, required, metav1.CreateOptions{})
		reportCreateEvent(c.eventRecorder, required, err)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	return existing.DeepCopy(), false, nil
}

func (c *CSIDriverOperatorCRController) syncConditions(ctx context.Context, conditions []operatorapi.OperatorCondition, newGeneration *applyoperatorv1.GenerationStatusApplyConfiguration) error {
	// Available condition
	availableCnd := applyoperatorv1.
		OperatorCondition().
		WithType(c.crConditionName(operatorapi.OperatorStatusTypeAvailable))
	disabled, msg := c.hasDisabledCondition(conditions)
	if disabled && c.allowDisabled {
		// The driver can't be running. Mark the operator as Available, but with an extra message.
		availableCnd = availableCnd.
			WithStatus(operatorapi.ConditionTrue).
			WithReason("DriverDisabled").
			WithMessage(fmt.Sprintf("CSI driver for %s is disabled: %s", c.name, msg))
	} else {
		// The driver should be running, copy conditions from the CR
		clusterCSIDriverAvailableCnd := status.UnionCondition(operatorapi.OperatorStatusTypeAvailable, operatorapi.ConditionTrue, nil, conditions...)
		if clusterCSIDriverAvailableCnd.Status == operatorapi.ConditionUnknown {
			// If the ClusterCSIDriver's Available condition is Unknown, then ours will be false
			availableCnd = availableCnd.
				WithStatus(operatorapi.ConditionFalse).
				WithReason("WaitForOperator").
				WithMessage(fmt.Sprintf("Waiting for %s operator to report status", c.name))
		} else {
			// Copy the ClusterCSIDriver's Available condition as is
			availableCnd = availableCnd.
				WithReason(clusterCSIDriverAvailableCnd.Reason).
				WithStatus(clusterCSIDriverAvailableCnd.Status).
				WithMessage(clusterCSIDriverAvailableCnd.Message)
		}
	}

	// Progressing condition
	progressingCnd := applyoperatorv1.OperatorCondition().WithType(c.crConditionName(operatorapi.OperatorStatusTypeProgressing))
	clusterCSIDriverProgressingCnd := status.UnionCondition(operatorapi.OperatorStatusTypeProgressing, operatorapi.ConditionFalse, nil, conditions...)
	if clusterCSIDriverProgressingCnd.Status == operatorapi.ConditionUnknown {
		if disabled && c.allowDisabled {
			progressingCnd = progressingCnd.WithStatus(operatorapi.ConditionFalse)
		} else {
			progressingCnd = progressingCnd.
				WithStatus(operatorapi.ConditionTrue).
				WithReason("WaitForOperator").
				WithMessage(fmt.Sprintf("Waiting for %s operator to report status", c.name))
		}
	} else {
		progressingCnd = progressingCnd.
			WithReason(clusterCSIDriverProgressingCnd.Reason).
			WithStatus(clusterCSIDriverProgressingCnd.Status).
			WithMessage(clusterCSIDriverProgressingCnd.Message)
	}

	// Upgradeable condition
	upgradeableConditionType := c.crConditionName(operatorapi.OperatorStatusTypeUpgradeable)
	upgradeableCnd := applyoperatorv1.OperatorCondition().
		WithType(upgradeableConditionType).
		WithStatus(operatorapi.ConditionTrue)

	if hasCondition(conditions, operatorapi.OperatorStatusTypeUpgradeable) {
		clusterCSIDriverUpgradeableCnd := status.UnionCondition(operatorapi.OperatorStatusTypeUpgradeable, operatorapi.ConditionTrue, nil, conditions...)
		upgradeableCnd = upgradeableCnd.
			WithReason(clusterCSIDriverUpgradeableCnd.Reason).
			WithStatus(clusterCSIDriverUpgradeableCnd.Status).
			WithMessage(clusterCSIDriverUpgradeableCnd.Message)
	}

	// Degraded condition
	degradedCnd := applyoperatorv1.OperatorCondition().WithType(c.crConditionName(operatorapi.OperatorStatusTypeDegraded))
	clusterCSIDriverDegradedCnd := status.UnionCondition(operatorapi.OperatorStatusTypeDegraded, operatorapi.ConditionFalse, nil, conditions...)
	if clusterCSIDriverDegradedCnd.Status == operatorapi.ConditionUnknown {
		degradedCnd = degradedCnd.WithStatus(operatorapi.ConditionFalse)
	} else {
		degradedCnd = degradedCnd.
			WithStatus(clusterCSIDriverDegradedCnd.Status).
			WithReason(clusterCSIDriverDegradedCnd.Reason).
			WithMessage(clusterCSIDriverDegradedCnd.Message)

	}

	// Create a partial status with conditions and newGeneration
	status := applyoperatorv1.OperatorStatus().
		WithConditions(
			availableCnd,
			progressingCnd,
			degradedCnd,
			upgradeableCnd).
		WithGenerations(newGeneration)

	return c.operatorClient.ApplyOperatorStatus(
		ctx,
		factory.ControllerFieldManager(c.name, "updateOperatorStatus"),
		status,
	)
}

func (c *CSIDriverOperatorCRController) hasDisabledCondition(conditions []operatorapi.OperatorCondition) (bool, string) {
	for i := range conditions {
		if strings.HasSuffix(conditions[i].Type, "Disabled") {
			return true, conditions[i].Message
		}
	}
	return false, ""
}

func hasCondition(conditions []operatorapi.OperatorCondition, conditionType string) bool {
	for _, condition := range conditions {
		if strings.HasSuffix(condition.Type, conditionType) {
			return true
		}
	}
	return false
}

func readClusterCSIDriverOrDie(objBytes []byte) *operatorapi.ClusterCSIDriver {
	requiredObj, err := runtime.Decode(opCodecs.UniversalDecoder(operatorapi.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*operatorapi.ClusterCSIDriver)
}
