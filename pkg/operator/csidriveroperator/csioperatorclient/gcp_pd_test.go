package csioperatorclient

import (
	"os"
	"testing"

	"github.com/openshift/cluster-storage-operator/assets"
)

// When GetGCPPDCSIOperatorConfig is called for standalone, it should reference
// only assets that exist in the embedded standalone asset tree.
func TestGetGCPPDCSIOperatorConfig_Standalone_AssetsExist(t *testing.T) {
	cfg := GetGCPPDCSIOperatorConfig(false)

	if cfg.CSIDriverName != GCPPDCSIDriverName {
		t.Errorf("expected CSIDriverName %q, got %q", GCPPDCSIDriverName, cfg.CSIDriverName)
	}
	if cfg.MgmtOperatorConfigAsset != "" {
		t.Errorf("expected no MgmtOperatorConfigAsset for standalone config, got %q", cfg.MgmtOperatorConfigAsset)
	}
	if len(cfg.MgmtStaticAssets) != 0 {
		t.Errorf("expected no MgmtStaticAssets for standalone config, got %v", cfg.MgmtStaticAssets)
	}

	assertAssetsExist(t, cfg.StandaloneOperatorConfigAsset)
	assertAssetsExist(t, cfg.CRAsset, cfg.DeploymentAsset)
	assertAssetsExist(t, cfg.StaticAssets...)
}

// When GetGCPPDCSIOperatorConfig is called for HyperShift, it should reference
// only assets that exist in the embedded hypershift mgmt/guest asset trees.
func TestGetGCPPDCSIOperatorConfig_Hypershift_AssetsExist(t *testing.T) {
	cfg := GetGCPPDCSIOperatorConfig(true)

	if cfg.CSIDriverName != GCPPDCSIDriverName {
		t.Errorf("expected CSIDriverName %q, got %q", GCPPDCSIDriverName, cfg.CSIDriverName)
	}
	if cfg.StandaloneOperatorConfigAsset != "" {
		t.Errorf("expected no StandaloneOperatorConfigAsset for hypershift config, got %q", cfg.StandaloneOperatorConfigAsset)
	}
	if cfg.MgmtOperatorConfigAsset == "" {
		t.Error("expected a MgmtOperatorConfigAsset for hypershift config")
	}
	if len(cfg.MgmtStaticAssets) == 0 {
		t.Error("expected MgmtStaticAssets to be populated for hypershift config")
	}

	assertAssetsExist(t, cfg.MgmtOperatorConfigAsset)
	assertAssetsExist(t, cfg.CRAsset, cfg.DeploymentAsset)
	assertAssetsExist(t, cfg.StaticAssets...)
	assertAssetsExist(t, cfg.MgmtStaticAssets...)
}

// When the driver control plane image env var is set, GetGCPPDCSIOperatorConfig
// should propagate it into the ImageReplacer for hypershift deployments.
func TestGetGCPPDCSIOperatorConfig_Hypershift_ImageReplacer(t *testing.T) {
	const wantImage = "quay.io/example/gcp-pd-csi-driver:test"
	t.Setenv(envGCPPDDriverControlPlaneImage, wantImage)

	cfg := GetGCPPDCSIOperatorConfig(true)
	got := cfg.ImageReplacer.Replace("${DRIVER_CONTROL_PLANE_IMAGE}")
	if got != wantImage {
		t.Errorf("expected replaced image %q, got %q", wantImage, got)
	}
}

func assertAssetsExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := assets.ReadFile(p); err != nil {
			t.Errorf("expected asset %q to exist: %v", p, err)
		}
	}
}

func init() {
	// Ensure a deterministic environment for the non-image-replacer tests,
	// in case the test binary inherits GCP_PD_* variables from the shell.
	for _, e := range []string{envGCPPDDriverOperatorImage, envGCPPDDriverImage, envGCPPDDriverControlPlaneImage} {
		if err := os.Unsetenv(e); err != nil {
			panic(err)
		}
	}
}
