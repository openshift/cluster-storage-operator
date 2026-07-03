package csidriveroperator

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/library-go/pkg/crypto"
	sigsyaml "sigs.k8s.io/yaml"
)

// tlsSettingsFromProfile returns minTLSVersion and IANA cipher suite names from a TLS
// security profile, defaulting to Intermediate if nil, empty, or unknown.
func tlsSettingsFromProfile(profile *configv1.TLSSecurityProfile) (string, []string) {
	if profile == nil || profile.Type == "" {
		spec := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		return string(spec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(spec.Ciphers)
	}
	if profile.Type == configv1.TLSProfileCustomType {
		if profile.Custom == nil {
			spec := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
			return string(spec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(spec.Ciphers)
		}
		return string(profile.Custom.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(profile.Custom.Ciphers)
	}
	spec, ok := configv1.TLSProfiles[profile.Type]
	if !ok || spec == nil {
		spec = configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
	return string(spec.MinTLSVersion), crypto.OpenSSLToIANACipherSuites(spec.Ciphers)
}

// operatorConfigYAML produces a minimal GenericOperatorConfig YAML with only
// the TLS fields set, omitting all zero-value fields that the typed struct would emit.
func operatorConfigYAML(minTLSVersion string, cipherSuites []string) (string, error) {
	cfg := map[string]interface{}{
		"apiVersion": operatorv1alpha1.SchemeGroupVersion.String(),
		"kind":       "GenericOperatorConfig",
		"servingInfo": map[string]interface{}{
			"minTLSVersion": minTLSVersion,
			"cipherSuites":  cipherSuites,
		},
	}
	data, err := sigsyaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to serialize GenericOperatorConfig: %w", err)
	}
	return string(data), nil
}
