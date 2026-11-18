package csidriveroperator

import (
	"strings"
	"testing"

	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// TestHyperShiftMgmtDeploymentAssetControlPlaneImageEnvVars checks that the
// embedded HyperShift management Deployment of each CSI driver operator passes
// the control-plane image placeholders to the operator container. CSO replaces
// them with its own environment variables (sidecarReplacer and the driver's
// ImageReplacer), and the CSI driver operator uses them for the controller
// containers that run in the hosted control plane. Without them the controller
// keeps the data-plane images, e.g. for its kube-rbac-proxy sidecars.
func TestHyperShiftMgmtDeploymentAssetControlPlaneImageEnvVars(t *testing.T) {
	wantEnv := []corev1.EnvVar{
		{Name: "DRIVER_CONTROL_PLANE_IMAGE", Value: "${DRIVER_CONTROL_PLANE_IMAGE}"},
		{Name: "LIVENESS_PROBE_CONTROL_PLANE_IMAGE", Value: "${LIVENESS_PROBE_CONTROL_PLANE_IMAGE}"},
		{Name: "KUBE_RBAC_PROXY_CONTROL_PLANE_IMAGE", Value: "${KUBE_RBAC_PROXY_CONTROL_PLANE_IMAGE}"},
	}

	tests := []struct {
		name   string
		config csioperatorclient.CSIOperatorConfig
		// container is the name of the CSI driver operator container. It is
		// not necessarily the first container: aws-ebs runs token-minter first.
		container string
	}{
		{
			name:      "aws-ebs",
			config:    csioperatorclient.GetAWSEBSCSIOperatorConfig(true),
			container: "aws-ebs-csi-driver-operator",
		},
		{
			name:      "azure-disk",
			config:    csioperatorclient.GetAzureDiskCSIOperatorConfig(true),
			container: "azure-disk-csi-driver-operator",
		},
		{
			name:      "azure-file",
			config:    csioperatorclient.GetAzureFileCSIOperatorConfig(true),
			container: "azure-file-csi-driver-operator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assetName := tt.config.DeploymentAsset
			if !strings.Contains(assetName, "/hypershift/mgmt/generated/") {
				t.Fatalf("expected a generated HyperShift management Deployment asset, got %q", assetName)
			}

			assetBytes, err := assets.ReadFile(assetName)
			if err != nil {
				t.Fatalf("failed to read asset %q: %v", assetName, err)
			}

			var deployment appsv1.Deployment
			if err := sigsyaml.Unmarshal(assetBytes, &deployment); err != nil {
				t.Fatalf("failed to decode Deployment from asset %q: %v", assetName, err)
			}

			var container *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == tt.container {
					container = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			if container == nil {
				t.Fatalf("container %q not found in asset %q", tt.container, assetName)
			}

			for _, want := range wantEnv {
				env := findEnvVar(want.Name, container.Env)
				if assert.NotNil(t, env, "env var %s missing from container %q in asset %q", want.Name, tt.container, assetName) {
					assert.Equal(t, want, *env, "unexpected env var %s in container %q", want.Name, tt.container)
				}
			}
		})
	}
}
