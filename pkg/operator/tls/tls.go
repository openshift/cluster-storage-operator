package tls

import (
	"fmt"
	"slices"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"k8s.io/klog/v2"
	sigsyaml "sigs.k8s.io/yaml"
)

// TLSSettingsFromProfile returns minTLSVersion, IANA cipher suite names and
// TLS group names from a TLS security profile, defaulting to Intermediate
// if nil, empty, or unknown. TLS groups are only returned when the
// TLSGroupPreferences feature gate is enabled.
func TLSSettingsFromProfile(profile *configv1.TLSSecurityProfile, featureGates featuregates.FeatureGate) (string, []string, []string) {
	spec := resolveProfileSpec(profile)
	return string(spec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(spec.Ciphers), tlsGroupsFromSpec(spec, featureGates)
}

// resolveProfileSpec returns the effective TLSProfileSpec for a TLSSecurityProfile.
// Named profiles resolve from the well-known openshift/api table; Custom uses its
// inline spec. Nil/empty/unknown profiles fall back to Intermediate.
func resolveProfileSpec(profile *configv1.TLSSecurityProfile) *configv1.TLSProfileSpec {
	if profile == nil || profile.Type == "" {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
	if profile.Type == configv1.TLSProfileCustomType {
		if profile.Custom == nil {
			return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		}
		return &profile.Custom.TLSProfileSpec
	}
	spec, ok := configv1.TLSProfiles[profile.Type]
	if !ok || spec == nil {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
	return spec
}

// tlsGroupsFromSpec returns TLS group names from the profile spec when the
// TLSGroupPreferences feature gate is enabled; otherwise nil.
func tlsGroupsFromSpec(spec *configv1.TLSProfileSpec, featureGates featuregates.FeatureGate) []string {
	if featureGates == nil || !slices.Contains(featureGates.KnownFeatures(), features.FeatureGateTLSGroupPreferences) {
		return nil
	}
	if !featureGates.Enabled(features.FeatureGateTLSGroupPreferences) {
		return nil
	}
	groups := make([]string, 0, len(spec.Groups))
	for _, g := range spec.Groups {
		groups = append(groups, string(g))
	}
	return groups
}

// OperatorConfigYAML produces a minimal GenericOperatorConfig YAML with only
// the TLS fields set (TLS group names are converted to numeric curve IDs),
// omitting all zero-value fields that the typed struct would emit.
func OperatorConfigYAML(minTLSVersion string, cipherSuites, tlsGroups []string) (string, error) {
	servingInfo := map[string]interface{}{
		"minTLSVersion": minTLSVersion,
		"cipherSuites":  cipherSuites,
	}
	// servingInfo.curvePreferences needs numeric crypto/tls curve IDs, not group names.
	curvePreferences, unrecognized := crypto.TLSGroupsToCurvePreferences(tlsGroups)
	if len(unrecognized) > 0 {
		klog.Warningf("Ignoring unrecognized TLS groups: %v", unrecognized)
	}
	if len(curvePreferences) > 0 {
		servingInfo["curvePreferences"] = curvePreferences
	}

	cfg := map[string]interface{}{
		"apiVersion":  operatorv1alpha1.SchemeGroupVersion.String(),
		"kind":        "GenericOperatorConfig",
		"servingInfo": servingInfo,
	}
	data, err := sigsyaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to serialize GenericOperatorConfig: %w", err)
	}
	return string(data), nil
}
