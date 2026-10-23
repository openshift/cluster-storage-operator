package volumedeviceexporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	volumeDeviceExporterImage   = "VOLUME_DEVICE_EXPORTER_IMAGE"
	kubeRBACProxyImage          = "KUBE_RBAC_PROXY_IMAGE"
	featureGateStoragePathAlert = configv1.FeatureGateName("StoragePathAlert") // TODO(STOR-3187): use features.FeatureGateStoragePathAlert
	metricsClientCAConfigMap    = "metrics-client-ca"
	metricsClientCAKey          = "ca-bundle.crt"
	exporterClientCAConfigMap   = "volume-device-exporter-client-ca"
)

var securityContextConstraintsGVR = schema.GroupVersionResource{Group: "security.openshift.io", Version: "v1", Resource: "securitycontextconstraints"}

var staticAssets = []string{
	"volumedeviceexporter/01_serviceaccount.yaml",
	"volumedeviceexporter/02_service-ca.yaml",
	"volumedeviceexporter/03_auth_proxy_clusterrole.yaml",
	"volumedeviceexporter/04_auth_proxy_clusterrolebinding.yaml",
	"volumedeviceexporter/09_privileged_scc_rolebinding.yaml",
	"volumedeviceexporter/03_service.yaml",
	"volumedeviceexporter/04_servicemonitor.yaml",
}

type controller struct {
	operatorClient v1helpers.OperatorClient
	clients        *csoclients.Clients
	featureGates   featuregates.FeatureGate
	recorder       events.Recorder
}

func NewControllers(clients *csoclients.Clients, featureGates featuregates.FeatureGate, recorder events.Recorder) []factory.Controller {
	c := &controller{
		operatorClient: clients.OperatorClient,
		clients:        clients,
		featureGates:   featureGates,
		recorder:       recorder.WithComponentSuffix("VolumeDeviceExporter"),
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
		AddInformer(clients.MonitoringInformer.Monitoring().V1().ServiceMonitors().Informer())

	daemonSetController := factory.New().WithSync(c.syncDaemonSet).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().DaemonSets().Informer(),
		clients.KubeInformers.InformersFor(csoclients.MonitoringNamespace).Core().V1().ConfigMaps().Informer(),
		clients.ConfigInformers.Config().V1().FeatureGates().Informer(),
	).ToController("VolumeDeviceExporterDaemonSet", c.recorder)

	return []factory.Controller{staticController, daemonSetController}
}

func (c *controller) syncDaemonSet(ctx context.Context, syncCtx factory.SyncContext) error {
	spec, _, _, err := c.operatorClient.GetOperatorState()
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if spec.ManagementState != operatorapi.Managed {
		return nil
	}
	if err := c.syncMetricsClientCA(ctx); err != nil {
		return err
	}
	return c.applyDaemonSet(ctx, spec)
}

func (c *controller) syncMetricsClientCA(ctx context.Context) error {
	source, err := c.clients.KubeInformers.InformersFor(csoclients.MonitoringNamespace).Core().V1().ConfigMaps().Lister().ConfigMaps(csoclients.MonitoringNamespace).Get(metricsClientCAConfigMap)
	if err != nil {
		return fmt.Errorf("get metrics client CA: %w", err)
	}
	required, err := clientCAConfigMap(source)
	if err != nil {
		return err
	}
	_, _, err = resourceapply.ApplyConfigMap(ctx, c.clients.KubeClient.CoreV1(), c.recorder, required)
	return err
}

func clientCAConfigMap(source *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	caBundle := ""
	for _, key := range []string{metricsClientCAKey, "client-ca.crt", "ca.crt", "service-ca.crt"} {
		if source.Data[key] != "" {
			caBundle = source.Data[key]
			break
		}
	}
	if caBundle == "" {
		return nil, fmt.Errorf("ConfigMap %s/%s has no supported CA bundle key", csoclients.MonitoringNamespace, metricsClientCAConfigMap)
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: exporterClientCAConfigMap, Namespace: csoclients.OperatorNamespace},
		Data:       map[string]string{metricsClientCAKey: caBundle},
	}, nil
}

func (c *controller) applyDaemonSet(ctx context.Context, spec *operatorapi.OperatorSpec) error {
	data, err := assets.ReadFile("volumedeviceexporter/05_daemonset.yaml")
	if err != nil {
		return err
	}
	minTLSVersion, cipherSuites := tlsProfile(spec.ObservedConfig.Raw)
	data = []byte(strings.NewReplacer(
		"${VOLUME_DEVICE_EXPORTER_IMAGE}", os.Getenv(volumeDeviceExporterImage),
		"${KUBE_RBAC_PROXY_IMAGE}", os.Getenv(kubeRBACProxyImage),
	).Replace(string(data)))
	daemonSet := resourceread.ReadDaemonSetV1OrDie(data)
	for i := range daemonSet.Spec.Template.Spec.Containers {
		container := &daemonSet.Spec.Template.Spec.Containers[i]
		if container.Name != "kube-rbac-proxy" {
			continue
		}
		container.Args = append(container.Args, "--tls-min-version="+minTLSVersion)
		if cipherSuites != "" {
			container.Args = append(container.Args, "--tls-cipher-suites="+cipherSuites)
		}
		break
	}
	_, _, err = resourceapply.ApplyDaemonSet(ctx, c.clients.KubeClient.AppsV1(), c.recorder, daemonSet, 0)
	return err
}

func tlsProfile(raw []byte) (string, string) {
	observed := map[string]interface{}{}
	if err := json.Unmarshal(raw, &observed); err != nil {
		return "VersionTLS12", ""
	}
	minTLSVersion, _, _ := unstructured.NestedString(observed, "targetconfig", "servingInfo", "minTLSVersion")
	cipherSuites, _, _ := unstructured.NestedStringSlice(observed, "targetconfig", "servingInfo", "cipherSuites")
	if minTLSVersion == "" {
		minTLSVersion = "VersionTLS12"
	}
	return minTLSVersion, strings.Join(cipherSuites, ",")
}
