package vsphereproblemdetector

import (
	"context"
	"fmt"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type monitoringController struct {
	operatorClient   v1helpers.OperatorClient
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	monitoringClient promclient.Interface
	eventRecorder    events.Recorder
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

func newMonitoringController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder,
	resyncInterval time.Duration) factory.Controller {

	c := &monitoringController{
		operatorClient: clients.OperatorClient,
		kubeClient:     clients.KubeClient,
		dynamicClient:  clients.DynamicClient,
		eventRecorder:  eventRecorder.WithComponentSuffix("vsphere-monitoring-controller"),
	}

	return factory.New().
		WithSync(c.sync).
		WithInformers(
			c.operatorClient.Informer(),
			clients.MonitoringInformer.Monitoring().V1().ServiceMonitors().Informer(),
			clients.MonitoringInformer.Monitoring().V1().PrometheusRules().Informer()).
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

	serviceMonitorBytes := generated.MustAsset("vsphere_problem_detector/11_service_monitor.yaml")

	_, err = resourceapply.ApplyServiceMonitor(c.dynamicClient, c.eventRecorder, serviceMonitorBytes)

	if err != nil {
		return err
	}

	prometheusRuleBytes := generated.MustAsset(prometheusRuleFile)
	requiredObj, _, err := genericCodec.Decode(prometheusRuleBytes, nil, nil)
	if err != nil {
		return fmt.Errorf("cannot decode %q: %v", prometheusRuleFile, err)
	}

	prometheusRule, ok := requiredObj.(*promv1.PrometheusRule)
	if ok {
		_, err := c.monitoringClient.MonitoringV1().
			PrometheusRules(prometheusRule.Namespace).Create(ctx, prometheusRule, metav1.CreateOptions{})
		return fmt.Errorf("failed to create prometheus rule: %v", err)
	}

	monitoringCondition := operatorapi.OperatorCondition{
		Type:   monitoringControllerName + operatorapi.OperatorStatusTypeAvailable,
		Status: operatorapi.ConditionTrue,
	}
	if _, _, err := v1helpers.UpdateStatus(c.operatorClient,
		v1helpers.UpdateConditionFn(monitoringCondition),
	); err != nil {
		return err
	}
	return nil
}
