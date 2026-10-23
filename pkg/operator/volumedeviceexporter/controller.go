package volumedeviceexporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

const (
	volumeDeviceExporterImage   = "VOLUME_DEVICE_EXPORTER_IMAGE"
	kubeRBACProxyImage          = "KUBE_RBAC_PROXY_IMAGE"
	metricsClientCAConfigMap    = "metrics-client-ca"
	exporterClientCAConfigMap   = "volume-device-exporter-client-ca"
	daemonSetControllerName     = "VolumeDeviceExporterDaemonSet"
	featureGateStoragePathAlert = configv1.FeatureGateName("StoragePathAlert") // TODO(STOR-3187): use features.FeatureGateStoragePathAlert once available.
)

var staticAssets = []string{
	"volumedeviceexporter/01_serviceaccount.yaml",
	"volumedeviceexporter/02_service-ca.yaml",
	"volumedeviceexporter/03_auth_proxy_clusterrole.yaml",
	"volumedeviceexporter/04_auth_proxy_clusterrolebinding.yaml",
	"volumedeviceexporter/09_privileged_scc_rolebinding.yaml",
	"volumedeviceexporter/03_service.yaml",
	"volumedeviceexporter/04_servicemonitor.yaml",
}

const prometheusRuleAsset = "volumedeviceexporter/10_prometheusrule.yaml"

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
	storagePathAlertEnabled := featureGates.Enabled(featureGateStoragePathAlert)
	assetsToApply := staticAssets
	if storagePathAlertEnabled {
		assetsToApply = append(append([]string{}, staticAssets...), prometheusRuleAsset)
	}
	staticController := staticresourcecontroller.NewStaticResourceController(
		"VolumeDeviceExporterStaticResources",
		assets.ReadFile,
		assetsToApply,
		resourceapply.NewKubeClientHolder(clients.KubeClient).WithDynamicClient(clients.DynamicClient),
		clients.OperatorClient,
		c.recorder,
	).
		AddKubeInformers(clients.KubeInformers).
		AddInformer(clients.MonitoringInformer.Monitoring().V1().ServiceMonitors().Informer())
	if storagePathAlertEnabled {
		staticController.AddInformer(clients.MonitoringInformer.Monitoring().V1().PrometheusRules().Informer())
	}
	controllers := []factory.Controller{staticController}
	if !storagePathAlertEnabled {
		return controllers
	}

	clientCAController := resourcesynccontroller.NewResourceSyncController(
		"VolumeDeviceExporterClientCA",
		clients.OperatorClient,
		clients.KubeInformers,
		clients.KubeClient.CoreV1(),
		clients.KubeClient.CoreV1(),
		c.recorder,
	)
	if err := clientCAController.SyncPartialConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: csoclients.OperatorNamespace, Name: exporterClientCAConfigMap},
		resourcesynccontroller.ResourceLocation{Namespace: csoclients.MonitoringNamespace, Name: metricsClientCAConfigMap},
		"client-ca.crt",
	); err != nil {
		panic(fmt.Sprintf("failed to configure volume device exporter client CA sync: %v", err))
	}

	daemonSetController := factory.New().
		WithControllerInstanceName(factory.ControllerInstanceName(daemonSetControllerName, "DaemonSet")).
		WithSync(c.syncDaemonSet).
		WithSyncDegradedOnError(clients.OperatorClient).
		WithInformers(
			clients.OperatorClient.Informer(),
			clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Apps().V1().DaemonSets().Informer(),
			clients.ConfigInformers.Config().V1().APIServers().Informer(),
		).
		ToController(daemonSetControllerName, c.recorder)

	return append(controllers, clientCAController, daemonSetController)
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
	data, err := assets.ReadFile("volumedeviceexporter/05_daemonset.yaml")
	if err != nil {
		return nil, err
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
		if c.featureGates.Enabled(features.FeatureGateTLSGroupPreferences) {
			curvePreferences, err := c.tlsCurvePreferences()
			if err != nil {
				return nil, err
			}
			if curvePreferences != "" {
				container.Args = append(container.Args, "--tls-curve-preferences="+curvePreferences)
			}
		}
		break
	}
	expectedGeneration := resourcemerge.ExpectedDaemonSetGeneration(daemonSet, status.Generations)
	actual, _, err := resourceapply.ApplyDaemonSet(ctx, c.clients.KubeClient.AppsV1(), c.recorder, daemonSet, expectedGeneration)
	return actual, err
}

func (c *controller) tlsCurvePreferences() (string, error) {
	apiServer, err := c.clients.ConfigInformers.Config().V1().APIServers().Lister().Get("cluster")
	if err != nil {
		return "", fmt.Errorf("get APIServer TLS profile: %w", err)
	}
	curves, unrecognized := tlsCurvePreferences(apiServer.Spec.TLSSecurityProfile)
	if len(unrecognized) > 0 {
		klog.Warningf("ignoring unrecognized TLS groups for volume device exporter: %v", unrecognized)
	}
	return curves, nil
}

func tlsCurvePreferences(profile *configv1.TLSSecurityProfile) (string, []configv1.TLSGroup) {
	profileType := configv1.TLSProfileIntermediateType
	if profile != nil && profile.Type != "" {
		profileType = profile.Type
	}
	var profileSpec *configv1.TLSProfileSpec
	if profileType == configv1.TLSProfileCustomType {
		if profile != nil && profile.Custom != nil {
			profileSpec = &profile.Custom.TLSProfileSpec
		}
	} else {
		profileSpec = configv1.TLSProfiles[profileType]
	}
	if profileSpec == nil {
		profileSpec = configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}

	curveIDs, unrecognized := crypto.TLSGroupsToCurvePreferences(profileSpec.Groups)
	curves := make([]string, len(curveIDs))
	for i, curveID := range curveIDs {
		curves[i] = strconv.Itoa(int(curveID))
	}
	return strings.Join(curves, ","), unrecognized
}

func (c *controller) updateStatus(ctx context.Context, daemonSet *appsv1.DaemonSet) error {
	available, progressing := daemonSetConditions(daemonSet)
	updateGeneration := func(newStatus *operatorapi.OperatorStatus) error {
		resourcemerge.SetDaemonSetGeneration(&newStatus.Generations, daemonSet)
		return nil
	}
	_, _, err := v1helpers.UpdateStatus(ctx, c.operatorClient,
		v1helpers.UpdateConditionFn(available),
		v1helpers.UpdateConditionFn(progressing),
		updateGeneration,
	)
	return err
}

func daemonSetConditions(daemonSet *appsv1.DaemonSet) (operatorapi.OperatorCondition, operatorapi.OperatorCondition) {
	available := operatorapi.OperatorCondition{
		Type:   daemonSetControllerName + operatorapi.OperatorStatusTypeAvailable,
		Status: operatorapi.ConditionTrue,
		Reason: "AsExpected",
	}
	if daemonSet.Status.NumberAvailable == 0 {
		available.Status = operatorapi.ConditionFalse
		available.Reason = "Deploying"
		available.Message = "Waiting for a DaemonSet pod to become available"
	}

	progressing := operatorapi.OperatorCondition{
		Type:   daemonSetControllerName + operatorapi.OperatorStatusTypeProgressing,
		Status: operatorapi.ConditionFalse,
		Reason: "AsExpected",
	}
	switch {
	case daemonSet.Generation != daemonSet.Status.ObservedGeneration:
		progressing.Status = operatorapi.ConditionTrue
		progressing.Reason = "Deploying"
		progressing.Message = "Waiting for DaemonSet to act on changes"
	case daemonSet.Status.UpdatedNumberScheduled < daemonSet.Status.DesiredNumberScheduled:
		progressing.Status = operatorapi.ConditionTrue
		progressing.Reason = "Deploying"
		progressing.Message = fmt.Sprintf("Waiting for DaemonSet to update %d node pods", daemonSet.Status.DesiredNumberScheduled)
	case daemonSet.Status.NumberUnavailable > 0:
		progressing.Status = operatorapi.ConditionTrue
		progressing.Reason = "Deploying"
		progressing.Message = "Waiting for DaemonSet to deploy node pods"
	}

	return available, progressing
}

func tlsProfile(raw []byte) (string, string) {
	observed := map[string]interface{}{}
	if err := json.Unmarshal(raw, &observed); err != nil {
		return string(configv1.VersionTLS12), ""
	}
	minTLSVersion, _, _ := unstructured.NestedString(observed, "targetconfig", "servingInfo", "minTLSVersion")
	cipherSuites, _, _ := unstructured.NestedStringSlice(observed, "targetconfig", "servingInfo", "cipherSuites")
	if minTLSVersion == "" {
		minTLSVersion = string(configv1.VersionTLS12)
	}
	return minTLSVersion, strings.Join(cipherSuites, ",")
}
