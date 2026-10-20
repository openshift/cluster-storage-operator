package csidriveroperator

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func tlsProfileHCP(profileType string, extras map[string]any) *unstructured.Unstructured {
	spec := map[string]any{}
	if profileType != "" {
		tlsProfile := map[string]any{"type": profileType}
		for k, v := range extras {
			tlsProfile[k] = v
		}
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

func ianaCiphers(profileType configv1.TLSProfileType) []string {
	return crypto.OpenSSLToIANACipherSuites(configv1.TLSProfiles[profileType].Ciphers)
}

func profileGroups(profileType configv1.TLSProfileType) []string {
	src := configv1.TLSProfiles[profileType].Groups
	out := make([]string, 0, len(src))
	for _, g := range src {
		out = append(out, string(g))
	}
	return out
}

func TestTLSSettingsFromHCP(t *testing.T) {
	fgEnabled := featuregates.NewFeatureGate(
		[]configv1.FeatureGateName{features.FeatureGateTLSGroupPreferences},
		nil,
	)
	fgDisabled := featuregates.NewFeatureGate(
		nil,
		[]configv1.FeatureGateName{features.FeatureGateTLSGroupPreferences},
	)

	tests := []struct {
		name           string
		hcp            *unstructured.Unstructured
		featureGates   featuregates.FeatureGate
		wantMinVersion string
		wantCiphers    []string
		wantGroups     []string
	}{
		{
			name:           "empty profile defaults to Intermediate, nil featureGates",
			hcp:            tlsProfileHCP("", nil),
			featureGates:   nil,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     nil,
		},
		{
			name:           "empty profile defaults to Intermediate, gate enabled",
			hcp:            tlsProfileHCP("", nil),
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
		{
			name:           "Intermediate profile, gate disabled omits groups",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileIntermediateType), nil),
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     nil,
		},
		{
			name:           "Intermediate profile, gate enabled",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileIntermediateType), nil),
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
		{
			name:           "Modern profile, gate disabled omits groups",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileModernType), nil),
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    ianaCiphers(configv1.TLSProfileModernType),
			wantGroups:     nil,
		},
		{
			name:           "Modern profile, gate enabled",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileModernType), nil),
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    ianaCiphers(configv1.TLSProfileModernType),
			wantGroups:     profileGroups(configv1.TLSProfileModernType),
		},
		{
			name:           "Old profile, gate enabled",
			hcp:            tlsProfileHCP(string(configv1.TLSProfileOldType), nil),
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS10),
			wantCiphers:    ianaCiphers(configv1.TLSProfileOldType),
			wantGroups:     profileGroups(configv1.TLSProfileOldType),
		},
		{
			name: "Custom profile without groups, gate enabled",
			hcp: tlsProfileHCP(string(configv1.TLSProfileCustomType), map[string]any{
				"custom": map[string]any{
					"minTLSVersion": "VersionTLS13",
					"ciphers":       []any{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-ECDSA-AES256-GCM-SHA384"},
				},
			}),
			featureGates:   fgEnabled,
			wantMinVersion: "VersionTLS13",
			wantCiphers: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
			},
			wantGroups: []string{},
		},
		{
			name: "Custom profile with groups, gate disabled omits groups",
			hcp: tlsProfileHCP(string(configv1.TLSProfileCustomType), map[string]any{
				"custom": map[string]any{
					"minTLSVersion": "VersionTLS12",
					"ciphers":       []any{"ECDHE-RSA-AES128-GCM-SHA256"},
					"groups":        []any{"X25519", "secp256r1"},
				},
			}),
			featureGates:   fgDisabled,
			wantMinVersion: "VersionTLS12",
			wantCiphers:    []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
			wantGroups:     nil,
		},
		{
			name: "Custom profile with groups, gate enabled",
			hcp: tlsProfileHCP(string(configv1.TLSProfileCustomType), map[string]any{
				"custom": map[string]any{
					"minTLSVersion": "VersionTLS12",
					"ciphers":       []any{"ECDHE-RSA-AES128-GCM-SHA256"},
					"groups":        []any{"X25519", "secp256r1"},
				},
			}),
			featureGates:   fgEnabled,
			wantMinVersion: "VersionTLS12",
			wantCiphers:    []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
			wantGroups:     []string{"X25519", "secp256r1"},
		},
		{
			name:           "unknown profile type falls back to Intermediate, gate enabled",
			hcp:            tlsProfileHCP("SomeUnknownProfile", nil),
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotCiphers, gotGroups, err := tlsSettingsFromHCP(tt.hcp, tt.featureGates)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantMinVersion, gotVersion)
			assert.Equal(t, tt.wantCiphers, gotCiphers)
			if tt.wantGroups == nil {
				assert.Nil(t, gotGroups)
			} else {
				assert.Equal(t, tt.wantGroups, gotGroups)
			}
		})
	}
}

func findEnvVar(name string, envVars []corev1.EnvVar) *corev1.EnvVar {
	for i := range envVars {
		if envVars[i].Name == name {
			return &envVars[i]
		}
	}
	return nil
}

func testDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "aws-ebs-csi-driver-operator",
							Env: []corev1.EnvVar{
								{Name: "DRIVER_IMAGE", Value: "quay.io/openshift/origin-aws-ebs-csi-driver:latest"},
							},
						},
					},
				},
			},
		},
	}
}

func TestInjectMgmtProxyEnvVars(t *testing.T) {
	tests := []struct {
		name       string
		httpProxy  string
		httpsProxy string
		noProxy    string
		validate   func(*testing.T, *appsv1.Deployment)
	}{
		{
			name:       "all proxy env vars are set",
			httpProxy:  "http://proxy.example.com:8080",
			httpsProxy: "https://proxy.example.com:8443",
			noProxy:    "localhost,127.0.0.1",
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				c := &deployment.Spec.Template.Spec.Containers[0]

				httpProxy := findEnvVar("HTTP_PROXY", c.Env)
				assert.NotNil(t, httpProxy)
				assert.Equal(t, "http://proxy.example.com:8080", httpProxy.Value)

				httpsProxy := findEnvVar("HTTPS_PROXY", c.Env)
				assert.NotNil(t, httpsProxy)
				assert.Equal(t, "https://proxy.example.com:8443", httpsProxy.Value)

				noProxy := findEnvVar("NO_PROXY", c.Env)
				assert.NotNil(t, noProxy)
				assert.Equal(t, "localhost,127.0.0.1", noProxy.Value)

				assert.NotNil(t, findEnvVar("DRIVER_IMAGE", c.Env), "existing env vars should be preserved")
			},
		},
		{
			name:      "only HTTP_PROXY is set",
			httpProxy: "http://proxy.example.com:8080",
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				c := &deployment.Spec.Template.Spec.Containers[0]

				assert.NotNil(t, findEnvVar("HTTP_PROXY", c.Env))
				assert.Nil(t, findEnvVar("HTTPS_PROXY", c.Env))
				assert.Nil(t, findEnvVar("NO_PROXY", c.Env))
			},
		},
		{
			name:       "only HTTPS_PROXY is set",
			httpsProxy: "https://proxy.example.com:8443",
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				c := &deployment.Spec.Template.Spec.Containers[0]

				assert.Nil(t, findEnvVar("HTTP_PROXY", c.Env))
				assert.NotNil(t, findEnvVar("HTTPS_PROXY", c.Env))
				assert.Nil(t, findEnvVar("NO_PROXY", c.Env))
			},
		},
		{
			name:    "only NO_PROXY is set",
			noProxy: "localhost,127.0.0.1",
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				c := &deployment.Spec.Template.Spec.Containers[0]

				assert.Nil(t, findEnvVar("HTTP_PROXY", c.Env))
				assert.Nil(t, findEnvVar("HTTPS_PROXY", c.Env))
				assert.NotNil(t, findEnvVar("NO_PROXY", c.Env))
			},
		},
		{
			name: "no proxy env vars are set",
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				c := &deployment.Spec.Template.Spec.Containers[0]

				assert.Nil(t, findEnvVar("HTTP_PROXY", c.Env))
				assert.Nil(t, findEnvVar("HTTPS_PROXY", c.Env))
				assert.Nil(t, findEnvVar("NO_PROXY", c.Env))

				assert.NotNil(t, findEnvVar("DRIVER_IMAGE", c.Env), "existing env vars should be preserved")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HTTP_PROXY", tt.httpProxy)
			t.Setenv("HTTPS_PROXY", tt.httpsProxy)
			t.Setenv("NO_PROXY", tt.noProxy)

			deployment := testDeployment()
			injectMgmtProxyEnvVars(deployment)
			tt.validate(t, deployment)
		})
	}
}
