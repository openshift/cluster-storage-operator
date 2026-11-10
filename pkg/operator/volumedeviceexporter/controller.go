package volumedeviceexporter

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/openshift/api/features"
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/csi/csidrivercontrollerservicecontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	volumeDeviceExporterImage = "VOLUME_DEVICE_EXPORTER_IMAGE"
	kubeRBACProxyImage        = "KUBE_RBAC_PROXY_IMAGE"
	daemonSetControllerName   = "VolumeDeviceExporterDaemonSet"
	clientCAControllerName    = "VolumeDeviceExporterClientCA"
	clientCAConfigMapName     = "volume-device-exporter-client-ca"
	clientCAConfigMapKey      = "client-ca-file"
)

var staticAssets = []string{
	"volumedeviceexporter/01_serviceaccount.yaml",
	"volumedeviceexporter/02_auth_proxy_clusterrole.yaml",
	"volumedeviceexporter/03_auth_proxy_clusterrolebinding.yaml",
	"volumedeviceexporter/04_privileged_scc_rolebinding.yaml",
	"volumedeviceexporter/05_service.yaml",
	"volumedeviceexporter/07_servicemonitor.yaml",
	"volumedeviceexporter/08_prometheusrule.yaml",
}

type controller struct {
	operatorClient v1helpers.OperatorClient
	clients        *csoclients.Clients
	recorder       events.Recorder
}

func NewControllers(clients *csoclients.Clients, featureGates featuregates.FeatureGate, recorder events.Recorder) []factory.Controller {
	c := &controller{
		operatorClient: clients.OperatorClient,
		clients:        clients,
		recorder:       recorder.WithComponentSuffix("VolumeDeviceExporter"),
	}
	if !slices.Contains(featureGates.KnownFeatures(), features.FeatureGateStoragePathAlert) ||
		!featureGates.Enabled(features.FeatureGateStoragePathAlert) {
		return nil
	}
	staticController := staticresourcecontroller.NewStaticResourceController(
		"VolumeDeviceExporterStaticResources",
		assets.ReadFile,
		staticAssets,
		resourceapply.NewKubeClientHolder(clients.KubeClient).WithDynamicClient(clients.DynamicClient),
		clients.OperatorClient,
		c.recorder,
	).
		AddKubeInformers(clients.KubeInformers).
		AddInformer(clients.MonitoringInformer.Monitoring().V1().ServiceMonitors().Informer()).
		AddInformer(clients.MonitoringInformer.Monitoring().V1().PrometheusRules().Informer())
	controllers := []factory.Controller{staticController, newClientCASyncController(clients, c.recorder)}

	daemonSetController := factory.New().
		WithControllerInstanceName(factory.ControllerInstanceName(daemonSetControllerName, "DaemonSet")).
		WithSync(c.syncDaemonSet).
		WithSyncDegradedOnError(clients.OperatorClient).
		WithInformers(
			clients.OperatorClient.Informer(),
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().DaemonSets().Informer(),
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().ConfigMaps().Informer(),
		).
		ToController(daemonSetControllerName, c.recorder)

	return append(controllers, daemonSetController)
}

func newClientCASyncController(clients *csoclients.Clients, recorder events.Recorder) *resourcesynccontroller.ResourceSyncController {
	clientCASyncController := resourcesynccontroller.NewResourceSyncController(
		clientCAControllerName,
		clients.OperatorClient,
		clients.KubeInformers,
		clients.KubeClient.CoreV1(),
		clients.KubeClient.CoreV1(),
		recorder,
	)
	// Trust ordinary API client certificates, not request-header proxy certificates.
	// Keep the source key; the DaemonSet maps it to the proxy's CA filename.
	if err := clientCASyncController.SyncPartialConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: csoclients.OperatorNamespace, Name: clientCAConfigMapName},
		resourcesynccontroller.ResourceLocation{Namespace: metav1.NamespaceSystem, Name: "extension-apiserver-authentication"},
		clientCAConfigMapKey,
	); err != nil {
		panic(fmt.Sprintf("configure volume device exporter client CA sync: %v", err))
	}

	return clientCASyncController
}

func (c *controller) syncDaemonSet(ctx context.Context, syncCtx factory.SyncContext) error {
	spec, status, _, err := c.operatorClient.GetOperatorState()
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if spec.ManagementState != operatorapi.Managed {
		return nil
	}

	daemonSet, err := c.applyDaemonSet(ctx, spec, status)
	if err != nil {
		return err
	}
	return c.updateStatus(ctx, daemonSet)
}

func (c *controller) applyDaemonSet(ctx context.Context, spec *operatorapi.OperatorSpec, status *operatorapi.OperatorStatus) (*appsv1.DaemonSet, error) {
	data, err := assets.ReadFile("volumedeviceexporter/06_daemonset.yaml")
	if err != nil {
		return nil, err
	}
	if len(spec.ObservedConfig.Raw) == 0 {
		return nil, fmt.Errorf("waiting for observed TLS configuration")
	}
	data, err = csidrivercontrollerservicecontroller.WithServingInfo()(spec, data)
	if err != nil {
		return nil, err
	}
	data = []byte(strings.NewReplacer(
		"${VOLUME_DEVICE_EXPORTER_IMAGE}", os.Getenv(volumeDeviceExporterImage),
		"${KUBE_RBAC_PROXY_IMAGE}", os.Getenv(kubeRBACProxyImage),
	).Replace(string(data)))
	daemonSet := resourceread.ReadDaemonSetV1OrDie(data)
	expectedGeneration := resourcemerge.ExpectedDaemonSetGeneration(daemonSet, status.Generations)
	actual, _, err := resourceapply.ApplyDaemonSet(ctx, c.clients.KubeClient.AppsV1(), c.recorder, daemonSet, expectedGeneration)
	return actual, err
}

func (c *controller) updateStatus(ctx context.Context, daemonSet *appsv1.DaemonSet) error {
	// The exporter does not affect storage availability. Report only reconciliation
	// errors through WithSyncDegradedOnError, not the readiness of its node pods.
	updateGeneration := func(newStatus *operatorapi.OperatorStatus) error {
		resourcemerge.SetDaemonSetGeneration(&newStatus.Generations, daemonSet)
		return nil
	}
	_, _, err := v1helpers.UpdateStatus(ctx, c.operatorClient,
		updateGeneration,
	)
	return err
}
