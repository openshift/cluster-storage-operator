package volumedeviceexporter

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/yaml"
)

func TestNewControllersFeatureGate(t *testing.T) {
	clients := csoclients.NewFakeClients(&csoclients.FakeTestObjects{})
	recorder := events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now()))

	for _, tc := range []struct {
		name            string
		enabled         []configv1.FeatureGateName
		disabled        []configv1.FeatureGateName
		controllerCount int
	}{
		{name: "enabled", enabled: []configv1.FeatureGateName{features.FeatureGateStoragePathAlert}, controllerCount: 2},
		{name: "disabled", disabled: []configv1.FeatureGateName{features.FeatureGateStoragePathAlert}, controllerCount: 0},
		{name: "unknown", controllerCount: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			featureGates := featuregates.NewFeatureGate(tc.enabled, tc.disabled)
			if got := len(NewControllers(clients, featureGates, recorder)); got != tc.controllerCount {
				t.Fatalf("controller count = %d, want %d", got, tc.controllerCount)
			}
		})
	}
}

func TestApplyDaemonSetTLS(t *testing.T) {
	for _, tc := range []struct {
		name           string
		observedConfig string
		wantTLSArgs    []string
		wantError      bool
	}{
		{
			name:           "without TLS groups",
			observedConfig: `{"targetcsiconfig":{"servingInfo":{"minTLSVersion":"VersionTLS12","cipherSuites":["TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"]}}}`,
			wantTLSArgs: []string{
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			},
		},
		{
			name:           "with TLS groups",
			observedConfig: `{"targetcsiconfig":{"servingInfo":{"minTLSVersion":"VersionTLS12","cipherSuites":["TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"],"curvePreferences":["secp256r1"]}}}`,
			wantTLSArgs: []string{
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"--tls-curve-preferences=23",
			},
		},
		{
			name:           "TLS 1.3 with empty cipher suites",
			observedConfig: `{"targetcsiconfig":{"servingInfo":{"minTLSVersion":"VersionTLS13","cipherSuites":[]}}}`,
			wantTLSArgs:    []string{"--tls-min-version=VersionTLS13"},
		},
		{name: "not yet observed", wantError: true},
		{name: "missing TLS config", observedConfig: `{}`, wantError: true},
		{name: "malformed config", observedConfig: `{`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := csoclients.NewFakeClients(&csoclients.FakeTestObjects{})
			c := &controller{
				clients:  clients,
				recorder: events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now())),
			}
			spec := &operatorapi.OperatorSpec{ObservedConfig: runtime.RawExtension{Raw: []byte(tc.observedConfig)}}
			daemonSet, err := c.applyDaemonSet(context.Background(), spec, &operatorapi.OperatorStatus{})
			if tc.wantError {
				if err == nil {
					t.Fatal("expected TLS configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if daemonSet.Spec.Template.Spec.PriorityClassName != "system-node-critical" {
				t.Errorf("priority class = %q, want system-node-critical", daemonSet.Spec.Template.Spec.PriorityClassName)
			}
			if _, exists := daemonSet.Spec.Template.Labels["priorityClassName"]; exists {
				t.Error("priorityClassName must be a pod spec field, not a label")
			}
			if got := daemonSet.Spec.Template.Annotations["target.workload.openshift.io/management"]; got != `{"effect": "PreferredDuringScheduling"}` {
				t.Errorf("management workload annotation = %q", got)
			}
			foundClientCAVolume := false
			for _, volume := range daemonSet.Spec.Template.Spec.Volumes {
				if volume.Name == "client-ca" {
					foundClientCAVolume = true
					if volume.ConfigMap == nil || volume.ConfigMap.Name != clientCAConfigMapName {
						t.Fatalf("client CA volume must reference %s: %#v", clientCAConfigMapName, volume)
					}
					if len(volume.ConfigMap.Items) != 1 || volume.ConfigMap.Items[0].Key != "client-ca.crt" || volume.ConfigMap.Items[0].Path != "client-ca.crt" {
						t.Errorf("unexpected client CA volume items: %v", volume.ConfigMap.Items)
					}
				}
			}
			if !foundClientCAVolume {
				t.Fatal("missing client CA volume")
			}
			for _, container := range daemonSet.Spec.Template.Spec.Containers {
				wantClientCA := container.Name == "kube-rbac-proxy"
				foundClientCAMount := false
				for _, mount := range container.VolumeMounts {
					if mount.Name == "client-ca" {
						foundClientCAMount = true
						if mount.MountPath != "/etc/tls/client" || !mount.ReadOnly || mount.SubPath != "" || mount.SubPathExpr != "" {
							t.Errorf("client CA must be mounted read-only as a directory for rotation: %#v", mount)
						}
					}
				}
				if foundClientCAMount != wantClientCA {
					t.Errorf("%s client CA mount present = %t, want %t", container.Name, foundClientCAMount, wantClientCA)
				}
				foundClientCAArg := false
				var tlsArgs []string
				for _, arg := range container.Args {
					if strings.HasPrefix(arg, "--client-ca-file") {
						foundClientCAArg = true
						if arg != "--client-ca-file=/etc/tls/client/client-ca.crt" {
							t.Errorf("unexpected client CA argument: %s", arg)
						}
					}
					if strings.Contains(arg, "${TLS_") {
						t.Fatalf("unresolved TLS placeholder in %s: %s", container.Name, arg)
					}
					if strings.HasPrefix(arg, "--tls-") && !strings.HasPrefix(arg, "--tls-cert-") && !strings.HasPrefix(arg, "--tls-private-key-") {
						tlsArgs = append(tlsArgs, arg)
					}
				}
				if foundClientCAArg != wantClientCA {
					t.Errorf("%s client CA argument present = %t, want %t", container.Name, foundClientCAArg, wantClientCA)
				}
				var want []string
				if container.Name == "kube-rbac-proxy" {
					want = tc.wantTLSArgs
				}
				if !reflect.DeepEqual(tlsArgs, want) {
					t.Errorf("%s TLS arguments = %v, want %v", container.Name, tlsArgs, want)
				}
			}
		})
	}
}

func TestServiceMonitorClientCertificateAuthentication(t *testing.T) {
	data, err := assets.ReadFile("volumedeviceexporter/04_servicemonitor.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var monitor monitoringv1.ServiceMonitor
	if err := yaml.UnmarshalStrict(data, &monitor); err != nil {
		t.Fatal(err)
	}
	if len(monitor.Spec.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(monitor.Spec.Endpoints))
	}
	endpoint := monitor.Spec.Endpoints[0]
	if endpoint.BearerTokenFile != "" || endpoint.Authorization != nil || endpoint.BearerTokenSecret != nil {
		t.Error("scraping must use client certificate authentication, not bearer tokens")
	}
	if endpoint.Scheme == nil || *endpoint.Scheme != "https" || endpoint.TLSConfig == nil {
		t.Fatal("scraping must use HTTPS with TLS configuration")
	}
	tls := endpoint.TLSConfig
	if tls.CertFile != "/etc/prometheus/secrets/metrics-client-certs/tls.crt" || tls.KeyFile != "/etc/prometheus/secrets/metrics-client-certs/tls.key" {
		t.Errorf("unexpected client certificate configuration: %#v", tls)
	}
	if tls.CAFile != "/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt" || tls.ServerName == nil || *tls.ServerName != "volume-device-exporter.openshift-cluster-storage-operator.svc" {
		t.Errorf("unexpected server verification configuration: %#v", tls)
	}
}

func TestUpdateStatusDoesNotReportPodReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status appsv1.DaemonSetStatus
	}{
		{
			name:   "initial deployment or upgrade",
			status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 1},
		},
		{
			name: "available",
			status: appsv1.DaemonSetStatus{
				ObservedGeneration:     1,
				DesiredNumberScheduled: 1,
				UpdatedNumberScheduled: 1,
				NumberAvailable:        1,
			},
		},
		{
			name: "node added",
			status: appsv1.DaemonSetStatus{
				ObservedGeneration: 1, DesiredNumberScheduled: 2,
				UpdatedNumberScheduled: 1, NumberAvailable: 1, NumberUnavailable: 1,
			},
		},
		{
			name: "node rebooted",
			status: appsv1.DaemonSetStatus{
				ObservedGeneration: 1, DesiredNumberScheduled: 1,
				UpdatedNumberScheduled: 1, NumberUnavailable: 1,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := csoclients.NewFakeClients(&csoclients.FakeTestObjects{})
			c := &controller{operatorClient: clients.OperatorClient}
			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-device-exporter", Namespace: csoclients.OperatorNamespace, Generation: 1,
				},
				Status: tc.status,
			}
			if err := c.updateStatus(context.Background(), daemonSet); err != nil {
				t.Fatal(err)
			}
			_, status, _, err := clients.OperatorClient.GetOperatorState()
			if err != nil {
				t.Fatal(err)
			}
			if len(status.Conditions) != 0 {
				t.Errorf("pod readiness must not affect operator conditions: %v", status.Conditions)
			}
			wantGenerations := []operatorapi.GenerationStatus{{
				Group: "apps", Resource: "daemonsets", Namespace: daemonSet.Namespace,
				Name: daemonSet.Name, LastGeneration: daemonSet.Generation,
			}}
			if !reflect.DeepEqual(status.Generations, wantGenerations) {
				t.Errorf("generations = %v, want %v", status.Generations, wantGenerations)
			}
		})
	}
}
