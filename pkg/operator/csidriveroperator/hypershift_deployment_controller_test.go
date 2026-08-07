package csidriveroperator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

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
