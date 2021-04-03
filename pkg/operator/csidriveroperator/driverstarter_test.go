package csidriveroperator

import (
	"testing"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
)

func TestShouldRunController(t *testing.T) {
	tests := []struct {
		name        string
		platform    v1.PlatformType
		featureGate *v1.FeatureGate
		config      csioperatorclient.CSIOperatorConfig
		expectRun   bool
	}{
		{
			"GA CSI driver on matching platform",
			v1.AWSPlatformType,
			featureSet(""),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "ebs.csi.aws.com",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "",
			},
			true,
		},
		{
			"GA CSI driver on non-matching platform",
			v1.GCPPlatformType,
			featureSet(""),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "ebs.csi.aws.com",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "",
			},
			false,
		},
		{
			"tech preview driver on non-matching platform",
			v1.VSpherePlatformType,
			featureSet("TechPreviewNoUpgrade"),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
		},
		{
			"tech preview driver with enabled TechPreviewNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			featureSet("TechPreviewNoUpgrade"),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			true,
		},
		{
			"tech preview driver with disabled TechPreviewNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			featureSet(""),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
		},
		{
			"tech preview driver with correct CustomNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			customSet("foo", "bar", "baz", "CSIDriverVSphere"),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			true,
		},
		{
			"tech preview driver with wrong CustomNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			customSet("foo", "bar", "baz"),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			infra := &v1.Infrastructure{
				Status: v1.InfrastructureStatus{
					PlatformStatus: &v1.PlatformStatus{
						Type: test.platform,
					},
				},
			}
			fg := test.featureGate
			cfg := test.config
			res := shouldRunController(cfg, infra, fg)
			if res != test.expectRun {
				t.Errorf("Expected run %t, got %t", test.expectRun, res)
			}
		})
	}
}

func featureSet(set v1.FeatureSet) *v1.FeatureGate {
	return &v1.FeatureGate{
		Spec: v1.FeatureGateSpec{
			FeatureGateSelection: v1.FeatureGateSelection{
				FeatureSet: set,
			},
		},
	}
}

func customSet(gates ...string) *v1.FeatureGate {
	return &v1.FeatureGate{
		Spec: v1.FeatureGateSpec{
			FeatureGateSelection: v1.FeatureGateSelection{
				FeatureSet: v1.CustomNoUpgrade,
				CustomNoUpgrade: &v1.CustomFeatureGates{
					Enabled: gates,
				},
			},
		},
	}
}
