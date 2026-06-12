package csidriveroperator

import (
	"maps"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func tlsProfileHCP(profileType string, extras map[string]any) *unstructured.Unstructured {
	spec := map[string]any{}
	if profileType != "" {
		tlsProfile := map[string]any{"type": profileType}
		maps.Copy(tlsProfile, extras)
		spec = map[string]any{
			"configuration": map[string]any{
				"apiServer": map[string]any{
					"tlsSecurityProfile": tlsProfile,
				},
			},
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
}

func TestTLSSettingsFromHCP(t *testing.T) {
	modernIANA := []string{
		"TLS_AES_128_GCM_SHA256",
		"TLS_AES_256_GCM_SHA384",
		"TLS_CHACHA20_POLY1305_SHA256",
	}
	intermediateIANA := []string{
		"TLS_AES_128_GCM_SHA256",
		"TLS_AES_256_GCM_SHA384",
		"TLS_CHACHA20_POLY1305_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
		"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
	}

	tests := []struct {
		name           string
		hcp            *unstructured.Unstructured
		wantMinVersion string
		wantCiphers    []string
	}{
		{
			name:           "empty profile defaults to Modern",
			hcp:            tlsProfileHCP("", nil),
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    modernIANA,
		},
		{
			name:           "Intermediate profile",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileIntermediateType), nil),
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    intermediateIANA,
		},
		{
			name:           "Modern profile",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileModernType), nil),
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    modernIANA,
		},
		{
			name: "Custom profile",
			hcp: tlsProfileHCP(string(configv1.TLSProfileCustomType), map[string]any{
				"custom": map[string]any{
					"minTLSVersion": "VersionTLS13",
					"ciphers":       []any{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-ECDSA-AES256-GCM-SHA384"},
				},
			}),
			wantMinVersion: "VersionTLS13",
			wantCiphers: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
			},
		},
		{
			name:           "unknown profile type falls back to Modern",
			hcp:            tlsProfileHCP("SomeUnknownProfile", nil),
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    modernIANA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotCiphers, err := tlsSettingsFromHCP(tt.hcp)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantMinVersion, gotVersion)
			assert.Equal(t, tt.wantCiphers, gotCiphers)
		})
	}
}
