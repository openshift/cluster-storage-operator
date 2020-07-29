package csidriveroperator

import (
	"context"
	"fmt"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	oplisters "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	optional               bool
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
		optional:               csiOperatorConfig.Optional,
	}
	return c
}

func (c *CSIDriverOperatorCRController) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("CSIDriverOperatorCRController sync started")
	defer klog.V(4).Infof("CSIDriverOperatorCRController sync finished")

	var errs []error
	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	// Sync CSIDriver CR
	requiredCR := c.getRequestedClusterCSIDriver(opSpec.LogLevel)
	var lastGeneration int64
	lastGenerationStatus := resourcemerge.GenerationFor(
		opStatus.Generations,
		schema.GroupResource{Group: operatorapi.GroupName, Resource: "clustercsidrivers"},
		"",
		requiredCR.Name)
	if lastGenerationStatus != nil {
		lastGeneration = lastGenerationStatus.LastGeneration
	}

	cr, _, err := c.applyClusterCSIDriver(requiredCR, lastGeneration)
	if err != nil {
		// This will set Degraded condition
		return err
	}

	newGeneration := operatorapi.GenerationStatus{
		Group:          operatorapi.GroupName,
		Resource:       "clustercsidrivers",
		Namespace:      cr.Namespace,
		Name:           cr.Name,
		LastGeneration: cr.ObjectMeta.Generation,
	}
	updateGenerationFn := func(newStatus *operatorapi.OperatorStatus) error {
		resourcemerge.SetGeneration(&opStatus.Generations, newGeneration)
		return nil
	}

	availableCnd := status.UnionCondition(operatorapi.OperatorStatusTypeAvailable, operatorapi.ConditionTrue, nil, cr.Status.Conditions...)
	availableCnd.Type = c.crConditionName(operatorapi.OperatorStatusTypeAvailable)
	if availableCnd.Status == operatorapi.ConditionUnknown {
		availableCnd.Status = operatorapi.ConditionFalse
		availableCnd.Reason = "WaitForOperator"
		availableCnd.Message = fmt.Sprintf("Waiting for %s operator to report status", c.name)
	}
	progressingCnd := status.UnionCondition(operatorapi.OperatorStatusTypeProgressing, operatorapi.ConditionFalse, nil, cr.Status.Conditions...)
	progressingCnd.Type = c.crConditionName(operatorapi.OperatorStatusTypeProgressing)
	degradedCnd := status.UnionCondition(operatorapi.OperatorStatusTypeDegraded, operatorapi.ConditionFalse, nil, cr.Status.Conditions...)
	degradedCnd.Type = c.crConditionName(operatorapi.OperatorStatusTypeDegraded)

	// TODO: handle optional CSI driver operators (set Available: true with a proper message?)

	if _, _, err := v1helpers.UpdateStatus(c.operatorClient,
		v1helpers.UpdateConditionFn(availableCnd),
		v1helpers.UpdateConditionFn(progressingCnd),
		v1helpers.UpdateConditionFn(degradedCnd),
		updateGenerationFn,
	); err != nil {
		errs = append(errs, err)
	}

	return errors.NewAggregate(errs)
}

func (c *CSIDriverOperatorCRController) getRequestedClusterCSIDriver(logLevel operatorapi.LogLevel) *operatorapi.ClusterCSIDriver {
	if logLevel == "" {
		logLevel = operatorapi.Normal
	}
	cr := readClusterCSIDriverOrDie(generated.MustAsset(c.csiDriverAsset))
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

func (c *CSIDriverOperatorCRController) applyClusterCSIDriver(requiredOriginal *operatorapi.ClusterCSIDriver, expectedGeneration int64) (*operatorapi.ClusterCSIDriver, bool, error) {
	err := resourceapply.SetSpecHashAnnotation(&requiredOriginal.ObjectMeta, requiredOriginal.Spec)
	if err != nil {
		return nil, false, err
	}

	required := requiredOriginal.DeepCopy()
	if required.Annotations == nil {
		required.Annotations = map[string]string{}
	}

	existing, err := c.operatorClientSet.OperatorV1().ClusterCSIDrivers().Get(context.TODO(), required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := c.operatorClientSet.OperatorV1().ClusterCSIDrivers().Create(context.TODO(), required, metav1.CreateOptions{})
		reportCreateEvent(c.eventRecorder, required, err)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := resourcemerge.BoolPtr(false)
	existingCopy := existing.DeepCopy()

	resourcemerge.EnsureObjectMeta(modified, &existingCopy.ObjectMeta, required.ObjectMeta)
	// there was no change to metadata, the generation was right, and we weren't asked for force the deployment
	if !*modified && existingCopy.ObjectMeta.Generation == expectedGeneration {
		return existingCopy, false, nil
	}

	// at this point we know that we're going to perform a write.  We're just trying to get the object correct
	toWrite := existingCopy // shallow copy so the code reads easier
	toWrite.Spec = *required.Spec.DeepCopy()
	if klog.V(4).Enabled() {
		klog.Infof("ClusterCSIDriver %q changes: %v", required.Name, resourceapply.JSONPatchNoError(existing, toWrite))
	}

	actual, err := c.operatorClientSet.OperatorV1().ClusterCSIDrivers().Update(context.TODO(), toWrite, metav1.UpdateOptions{})
	reportUpdateEvent(c.eventRecorder, required, err)
	return actual, true, err
}

func readClusterCSIDriverOrDie(objBytes []byte) *operatorapi.ClusterCSIDriver {
	requiredObj, err := runtime.Decode(opCodecs.UniversalDecoder(operatorapi.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*operatorapi.ClusterCSIDriver)
}
