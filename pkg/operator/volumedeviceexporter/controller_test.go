package volumedeviceexporter

import (
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	appsv1 "k8s.io/api/apps/v1"
	clocktesting "k8s.io/utils/clock/testing"
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
		{name: "enabled", enabled: []configv1.FeatureGateName{featureGateStoragePathAlert}, controllerCount: 3},
		{name: "disabled", disabled: []configv1.FeatureGateName{featureGateStoragePathAlert}, controllerCount: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			featureGates := featuregates.NewFeatureGate(tc.enabled, tc.disabled)
			if got := len(NewControllers(clients, featureGates, recorder)); got != tc.controllerCount {
				t.Fatalf("controller count = %d, want %d", got, tc.controllerCount)
			}
		})
	}
}

func TestTLSProfile(t *testing.T) {
	minVersion, ciphers := tlsProfile([]byte(`{"targetconfig":{"servingInfo":{"minTLSVersion":"VersionTLS13","cipherSuites":["TLS_AES_128_GCM_SHA256"]}}}`))
	if minVersion != "VersionTLS13" || ciphers != "TLS_AES_128_GCM_SHA256" {
		t.Fatalf("tlsProfile() = %q, %q", minVersion, ciphers)
	}

	minVersion, ciphers = tlsProfile(nil)
	if minVersion != "VersionTLS12" || ciphers != "" {
		t.Fatalf("tlsProfile(nil) = %q, %q", minVersion, ciphers)
	}
}

func TestTLSCurvePreferences(t *testing.T) {
	profile := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
			Groups: []configv1.TLSGroup{configv1.TLSGroupSecP256r1},
		}},
	}
	curves, unrecognized := tlsCurvePreferences(profile)
	if len(unrecognized) != 0 {
		t.Fatalf("unexpected unrecognized groups: %v", unrecognized)
	}
	if curves != "23" {
		t.Fatalf("curve preferences = %q, want %q", curves, "23")
	}
}

func TestDaemonSetConditions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      appsv1.DaemonSetStatus
		generation  int64
		available   operatorapi.ConditionStatus
		progressing operatorapi.ConditionStatus
	}{
		{
			name:        "deploying",
			generation:  1,
			status:      appsv1.DaemonSetStatus{ObservedGeneration: 0, DesiredNumberScheduled: 1},
			available:   operatorapi.ConditionFalse,
			progressing: operatorapi.ConditionTrue,
		},
		{
			name:       "available",
			generation: 1,
			status: appsv1.DaemonSetStatus{
				ObservedGeneration:     1,
				DesiredNumberScheduled: 1,
				UpdatedNumberScheduled: 1,
				NumberAvailable:        1,
			},
			available:   operatorapi.ConditionTrue,
			progressing: operatorapi.ConditionFalse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemonSet := &appsv1.DaemonSet{Status: tc.status}
			daemonSet.Generation = tc.generation
			available, progressing := daemonSetConditions(daemonSet)
			if available.Status != tc.available || progressing.Status != tc.progressing {
				t.Fatalf("conditions = Available %s, Progressing %s; want %s, %s", available.Status, progressing.Status, tc.available, tc.progressing)
			}
		})
	}
}
