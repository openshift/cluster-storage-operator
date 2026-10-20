package volumedeviceexporter

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestSupportedHyperShiftPlatform(t *testing.T) {
	for _, tc := range []struct {
		name     string
		platform *configv1.PlatformStatus
		want     bool
	}{
		{name: "nil", platform: nil, want: false},
		{name: "aws", platform: &configv1.PlatformStatus{Type: configv1.AWSPlatformType}, want: true},
		{name: "azure", platform: &configv1.PlatformStatus{Type: configv1.AzurePlatformType}, want: true},
		{name: "gcp", platform: &configv1.PlatformStatus{Type: configv1.GCPPlatformType}, want: true},
		{name: "vsphere", platform: &configv1.PlatformStatus{Type: configv1.VSpherePlatformType}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := supportedHyperShiftPlatform(tc.platform); got != tc.want {
				t.Fatalf("supportedHyperShiftPlatform() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestMetricsClientCAConfigMap(t *testing.T) {
	for _, key := range []string{metricsClientCAKey, "client-ca.crt", "ca.crt", "service-ca.crt"} {
		t.Run(key, func(t *testing.T) {
			configMap, err := clientCAConfigMap(&corev1.ConfigMap{Data: map[string]string{key: "test-ca"}})
			if err != nil {
				t.Fatal(err)
			}
			if configMap.Namespace != "openshift-cluster-storage-operator" || configMap.Name != exporterClientCAConfigMap || configMap.Data[metricsClientCAKey] != "test-ca" {
				t.Fatalf("unexpected metrics client CA ConfigMap: %#v", configMap)
			}
		})
	}

	if _, err := clientCAConfigMap(&corev1.ConfigMap{}); err == nil {
		t.Fatal("expected an error for a missing metrics client CA")
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
