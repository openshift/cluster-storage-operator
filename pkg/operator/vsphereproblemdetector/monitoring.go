package vsphereproblemdetector

import (
	"context"
	"fmt"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	ov1 "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	promscheme "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/scheme"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

type monitoringController struct {
	operatorClient   v1helpers.OperatorClient
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	configMapLister  v1.ConfigMapLister
	monitoringClient promclient.Interface
	eventRecorder    events.Recorder

	clusterCSIDriverLister ov1.ClusterCSIDriverLister
}

const (
	monitoringControllerName = "VSphereProblemDetectorMonitoringController"
	prometheusRuleFile       = "vsphere_problem_detector/12_prometheusrules.yaml"
)

var (
	genericScheme = runtime.NewScheme()
	genericCodecs = serializer.NewCodecFactory(genericScheme)
	genericCodec  = genericCodecs.UniversalDeserializer()
)

func init() {
	if err := promscheme.AddToScheme(genericScheme); err != nil {
		panic(err)
	}
}

func newMonitoringController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder,
	resyncInterval time.Duration) factory.Controller {

	c := &monitoringController{
		operatorClient:   clients.OperatorClient,
		kubeClient:       clients.KubeClient,
		dynamicClient:    clients.DynamicClient,
		configMapLister:  clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		eventRecorder:    eventRecorder.WithComponentSuffix("vsphere-monitoring-controller"),
		monitoringClient: clients.MonitoringClient,

		clusterCSIDriverLister: clients.OperatorInformers.Operator().V1().ClusterCSIDrivers().Lister(),
	}

	return factory.New().
		WithSync(c.sync).
		WithInformers(
			c.operatorClient.Informer(),
			clients.MonitoringInformer.Monitoring().V1().ServiceMonitors().Informer(),
			clients.MonitoringInformer.Monitoring().V1().PrometheusRules().Informer(),
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().ConfigMaps().Informer(),
			clients.OperatorInformers.Operator().V1().ClusterCSIDrivers().Informer()).
		ResyncEvery(resyncInterval).
		WithSyncDegradedOnError(clients.OperatorClient).
		ToController(monitoringControllerName, c.eventRecorder)
}

func (c *monitoringController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}
	smBytes, err := assets.ReadFile("vsphere_problem_detector/11_service_monitor.yaml")
	if err != nil {
		return err
	}
	serviceMonitor := resourceread.ReadUnstructuredOrDie(smBytes)
	_, _, err = resourceapply.ApplyServiceMonitor(ctx, c.dynamicClient, c.eventRecorder, serviceMonitor)
	if err != nil {
		return err
	}

	cfg, err := ParseConfigMap(c.configMapLister)
	if err != nil {
		return err
	}

	ccd, err := c.clusterCSIDriverLister.Get(csioperatorclient.VMwareVSphereDriverName)
	if err != nil {
		return err
	}

	prometheusRuleBytes, err := assets.ReadFile(prometheusRuleFile)
	if err != nil {
		return err
	}

	var message string
	if cfg.AlertsDisabled || ccd.Spec.OperatorSpec.ManagementState == operatorapi.Removed {
		err = c.deletePrometheusRule(ctx, prometheusRuleBytes)
		if err != nil {
			return err
		}
		message = "vsphere-problem-detector alerts are disabled"
	} else {
		_, _, err = c.syncPrometheusRule(ctx, prometheusRuleBytes)
		if err != nil {
			return err
		}
		message = "vsphere-problem-detector alerts are enabled"
	}

	monitoringCondition := applyoperatorv1.OperatorCondition().
		WithType(monitoringControllerName + operatorapi.OperatorStatusTypeAvailable).
		WithStatus(operatorapi.ConditionTrue).
		WithMessage(message)

	status := applyoperatorv1.OperatorStatus().WithConditions(monitoringCondition)
	return c.operatorClient.ApplyOperatorStatus(
		ctx,
		factory.ControllerFieldManager(monitoringControllerName, "updateOperatorStatus"),
		status,
	)
}

func (c *monitoringController) syncPrometheusRule(ctx context.Context, prometheusRuleBytes []byte) (*promv1.PrometheusRule, bool, error) {
	prometheusRule, err := c.parsePrometheusRule(prometheusRuleBytes)
	if err != nil {
		return prometheusRule, false, err
	}

	existingRule, err := c.monitoringClient.MonitoringV1().PrometheusRules(prometheusRule.Namespace).Get(ctx, prometheusRule.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		existingRule, err = c.monitoringClient.MonitoringV1().
			PrometheusRules(prometheusRule.Namespace).Create(ctx, prometheusRule, metav1.CreateOptions{})
		if err != nil {
			return nil, false, fmt.Errorf("failed to create prometheus rule: %v", err)
		}
		return existingRule, true, nil
	}

	existingRuleCopy := existingRule.DeepCopy()
	existingSpec := existingRuleCopy.Spec

	modified := resourcemerge.BoolPtr(false)

	resourcemerge.EnsureObjectMeta(modified, &existingRuleCopy.ObjectMeta, prometheusRule.ObjectMeta)
	contentSame := equality.Semantic.DeepEqual(existingSpec, prometheusRule.Spec)
	// no modifications are necessary everything is same
	if contentSame && !*modified {
		return existingRule, false, nil
	}

	prometheusRule.ObjectMeta = *existingRuleCopy.ObjectMeta.DeepCopy()
	prometheusRule.TypeMeta = existingRuleCopy.TypeMeta

	klog.V(4).Infof("prometheus rule %s is modified outside of openshift - updating", prometheusRuleFile)
	updatedRule, err := c.monitoringClient.MonitoringV1().PrometheusRules(prometheusRule.Namespace).Update(ctx, prometheusRule, metav1.UpdateOptions{})
	return updatedRule, true, err
}

func (c *monitoringController) deletePrometheusRule(ctx context.Context, prometheusRuleBytes []byte) error {
	prometheusRule, err := c.parsePrometheusRule(prometheusRuleBytes)
	if err != nil {
		return err
	}

	_, err = c.monitoringClient.MonitoringV1().PrometheusRules(prometheusRule.Namespace).Get(ctx, prometheusRule.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	c.monitoringClient.MonitoringV1().PrometheusRules(prometheusRule.Namespace).Delete(ctx, prometheusRule.Name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	klog.V(2).Infof("prometheus rule %s deleted", prometheusRuleFile)
	return nil
}

func (c *monitoringController) parsePrometheusRule(prometheusRuleBytes []byte) (*promv1.PrometheusRule, error) {
	requiredObj, _, err := genericCodec.Decode(prometheusRuleBytes, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot decode %q: %v", prometheusRuleFile, err)
	}

	prometheusRule, ok := requiredObj.(*promv1.PrometheusRule)
	if !ok {
		return nil, fmt.Errorf("invalid prometheusrule: %+v", requiredObj)
	}
	return prometheusRule, nil
}
