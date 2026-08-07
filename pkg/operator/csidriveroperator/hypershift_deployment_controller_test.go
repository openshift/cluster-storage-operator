package csidriveroperator

import (
	"maps"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
			name:           "empty profile defaults to Intermediate",
			hcp:            tlsProfileHCP("", nil),
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    intermediateIANA,
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
			name:           "unknown profile type falls back to Intermediate",
			hcp:            tlsProfileHCP("SomeUnknownProfile", nil),
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    intermediateIANA,
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
