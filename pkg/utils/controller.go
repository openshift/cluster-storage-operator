package utils

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	annOpenShiftManaged = "csi.openshift.io/managed"
)

// ShouldRunController returns true, if given CSI driver controller should run.
func ShouldRunController(cfg csioperatorclient.CSIOperatorConfig, infrastructure *configv1.Infrastructure, fg *configv1.FeatureGate, csiDriver *storagev1.CSIDriver) (bool, error) {
	// Check the correct platform first, it will filter out most CSI driver operators
	var platform configv1.PlatformType
	if infrastructure.Status.PlatformStatus != nil {
		platform = infrastructure.Status.PlatformStatus.Type
	}
	if cfg.Platform != csioperatorclient.AllPlatforms && cfg.Platform != platform {
		klog.V(5).Infof("Not starting %s: wrong platform %s", cfg.CSIDriverName, platform)
		return false, nil
	}

	if cfg.RequireFeatureGate == "" {
		// This is GA / always enabled operator, always run
		klog.V(5).Infof("Starting %s: it's GA", cfg.CSIDriverName)
		return true, nil
	}

	if !featureGateEnabled(fg, cfg.RequireFeatureGate) {
		klog.V(4).Infof("Not starting %s: feature %s is not enabled", cfg.CSIDriverName, cfg.RequireFeatureGate)
		return false, nil
	}

	if isUnsupportedCSIDriverRunning(cfg, csiDriver) {
		// Some other version of the CSI driver is running, degrade the whole cluster
		return false, fmt.Errorf("detected CSI driver %s that is not provided by OpenShift - please remove it before enabling the OpenShift one", cfg.CSIDriverName)
	}

	// Tech preview operator and tech preview is enabled
	klog.V(5).Infof("Starting %s: feature %s is enabled", cfg.CSIDriverName, cfg.RequireFeatureGate)
	return true, nil
}

// Get list of enabled feature fates from FeatureGate CR.
func getEnabledFeatures(fg *configv1.FeatureGate) []string {
	if fg.Spec.FeatureSet == "" {
		return nil
	}
	if fg.Spec.FeatureSet == configv1.CustomNoUpgrade {
		return fg.Spec.CustomNoUpgrade.Enabled
	}
	gates := configv1.FeatureSets[fg.Spec.FeatureSet]
	if gates == nil {
		return nil
	}
	return gates.Enabled
}

// featureGateEnabled returns true if a given feature is enabled in FeatureGate CR.
func featureGateEnabled(fg *configv1.FeatureGate, feature string) bool {
	enabledFeatures := getEnabledFeatures(fg)
	for _, f := range enabledFeatures {
		if f == feature {
			return true
		}
	}
	return false
}

func isUnsupportedCSIDriverRunning(cfg csioperatorclient.CSIOperatorConfig, csiDriver *storagev1.CSIDriver) bool {
	if csiDriver == nil {
		return false
	}

	if metav1.HasAnnotation(csiDriver.ObjectMeta, annOpenShiftManaged) {
		return false
	}

	return true
}
