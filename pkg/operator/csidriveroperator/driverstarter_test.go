package csidriveroperator

import (
	"testing"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldRunController(t *testing.T) {
	tests := []struct {
		name        string
		platform    v1.PlatformType
		featureGate *v1.FeatureGate
		csiDriver   *storagev1.CSIDriver
		config      csioperatorclient.CSIOperatorConfig
		expectRun   bool
		expectError bool
	}{
		{
			"tech preview Shared Resource driver on AllPlatforms type",
			v1.AWSPlatformType,
			featureSet("TechPreviewNoUpgrade"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           csioperatorclient.AllPlatforms,
				RequireFeatureGate: "CSIDriverSharedResource",
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver on AWSPlatformType",
			v1.AWSPlatformType,
			featureSet("TechPreviewNoUpgrade"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "CSIDriverSharedResource",
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver on GCPPlatformType",
			v1.GCPPlatformType,
			featureSet("TechPreviewNoUpgrade"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.GCPPlatformType,
				RequireFeatureGate: "CSIDriverSharedResource",
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver on GCPPlatformType",
			v1.VSpherePlatformType,
			featureSet("TechPreviewNoUpgrade"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverSharedResource",
			},
			true,
			false,
		},
		{
			"GA CSI driver on matching platform",
			v1.AWSPlatformType,
			featureSet(""),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "ebs.csi.aws.com",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "",
			},
			true,
			false,
		},
		{
			"GA CSI driver on non-matching platform",
			v1.GCPPlatformType,
			featureSet(""),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "ebs.csi.aws.com",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "",
			},
			false,
			false,
		},
		{
			"tech preview driver on non-matching platform",
			v1.VSpherePlatformType,
			featureSet("TechPreviewNoUpgrade"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
			false,
		},
		{
			"tech preview driver with enabled TechPreviewNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			featureSet("TechPreviewNoUpgrade"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			true,
			false,
		},
		{
			"tech preview driver with disabled TechPreviewNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			featureSet(""),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
			false,
		},
		{
			"tech preview driver with correct CustomNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			customSet("foo", "bar", "baz", "CSIDriverVSphere"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			true,
			false,
		},
		{
			"tech preview driver with wrong CustomNoUpgrade FeatureSet",
			v1.VSpherePlatformType,
			customSet("foo", "bar", "baz"),
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
			false,
		},
		{
			"tech preview driver with existing OpenShift CSIDriver",
			v1.VSpherePlatformType,
			customSet("CSIDriverVSphere"),
			csiDriver("vsphere", map[string]string{annOpenShiftManaged: "true"}),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			true,
			false,
		},
		{
			"tech preview driver with existing community CSIDriver",
			v1.VSpherePlatformType,
			customSet("CSIDriverVSphere"),
			csiDriver("vsphere", nil),
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vsphere",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: "CSIDriverVSphere",
			},
			false,
			true,
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
			res, err := shouldRunController(test.config, infra, test.featureGate, test.csiDriver)
			if res != test.expectRun {
				t.Errorf("Expected run %t, got %t", test.expectRun, res)
			}
			gotError := err != nil
			if gotError != test.expectError {
				t.Errorf("Expected error %t, got %t: %s", test.expectError, res, err)
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

func csiDriver(csiDriverName string, annotations map[string]string) *storagev1.CSIDriver {
	return &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name:        csiDriverName,
			Annotations: annotations,
		},
		Spec: storagev1.CSIDriverSpec{},
	}
}
