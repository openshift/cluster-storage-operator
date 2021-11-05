package servicemonitor

import (
	"context"
	"fmt"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/cluster-storage-operator/pkg/utils"
)

const (
	monitoringControllerName = "MonitoringController"
)

var (
	_ factory.Controller = &ServiceMonitorController{}
)

type ServiceMonitorController struct {
	operatorClient    v1helpers.OperatorClient
	infraLister       openshiftv1.InfrastructureLister
	featureGateLister openshiftv1.FeatureGateLister
	dynamicClient     dynamic.Interface
	eventRecorder     events.Recorder
	factory           *factory.Factory
	driverConfigs     []csioperatorclient.CSIOperatorConfig
}

func NewServiceMonitorController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
	driverConfigs []csioperatorclient.CSIOperatorConfig) factory.Controller {
	c := &ServiceMonitorController{
		operatorClient:    clients.OperatorClient,
		infraLister:       clients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		featureGateLister: clients.ConfigInformers.Config().V1().FeatureGates().Lister(),
		dynamicClient:     clients.DynamicClient,
		eventRecorder:     eventRecorder.WithComponentSuffix("monitoring-controller"),
		driverConfigs:     driverConfigs,
	}

	return factory.New().
		WithSync(c.Sync).
		WithInformers(
			c.operatorClient.Informer(),
			clients.MonitoringInformer.Monitoring().V1().ServiceMonitors().Informer(),
			clients.MonitoringInformer.Monitoring().V1().PrometheusRules().Informer()).
		ResyncEvery(resyncInterval).
		WithSyncDegradedOnError(clients.OperatorClient).
		ToController(monitoringControllerName, c.eventRecorder)
}

func (c *ServiceMonitorController) Name() string {
	return monitoringControllerName
}

func (c *ServiceMonitorController) Sync(ctx context.Context, syncContext factory.SyncContext) error {
	klog.V(4).Infof("MonitoringController sync started")
	defer klog.V(4).Infof("MonitoringController sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if kerrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	for _, config := range c.driverConfigs {

		infrastructure, err := c.infraLister.Get("cluster")
		if err != nil {
			return err
		}
		featureGate, err := c.featureGateLister.Get("cluster")
		if err != nil {
			return err
		}

		shouldRun, err := utils.ShouldRunController(config, infrastructure, featureGate, nil)
		if err != nil {
			return err
		}
		if !shouldRun {
			continue
		}

		objBytes, err := assets.ReadFile(config.ServiceMonitorAsset)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("missing %q: %v", config.ServiceMonitorAsset, err))
			continue
		}

		serviceMonitor := resourceread.ReadUnstructuredOrDie(objBytes)
		_, _, err = resourceapply.ApplyServiceMonitor(ctx, c.dynamicClient, c.eventRecorder, serviceMonitor)
		if err != nil {
			return err
		}

		monitoringCondition := operatorapi.OperatorCondition{
			Type:   c.Name() + operatorapi.OperatorStatusTypeAvailable,
			Status: operatorapi.ConditionTrue,
		}
		if _, _, err := v1helpers.UpdateStatus(context.TODO(), c.operatorClient,
			v1helpers.UpdateConditionFn(monitoringCondition),
		); err != nil {
			return err
		}
	}

	return nil
}

func (c *ServiceMonitorController) Run(ctx context.Context, workers int) {
	ctrl := c.factory.WithSync(c.Sync).ToController(c.Name(), c.eventRecorder)
	ctrl.Run(ctx, workers)
}
