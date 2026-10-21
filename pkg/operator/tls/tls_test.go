package tls

import (
	"crypto/tls"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/stretchr/testify/assert"
	sigsyaml "sigs.k8s.io/yaml"
)

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

func TestTLSSettingsFromProfile(t *testing.T) {
	fgEnabled := featuregates.NewFeatureGate(
		[]configv1.FeatureGateName{features.FeatureGateTLSGroupPreferences},
		nil,
	)
	fgDisabled := featuregates.NewFeatureGate(
		nil,
		[]configv1.FeatureGateName{features.FeatureGateTLSGroupPreferences},
	)

	customCiphers := []string{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-ECDSA-AES256-GCM-SHA384"}
	customGroups := []configv1.TLSGroup{configv1.TLSGroupX25519, configv1.TLSGroupSecP256r1}

	tests := []struct {
		name           string
		profile        *configv1.TLSSecurityProfile
		featureGates   featuregates.FeatureGate
		wantMinVersion string
		wantCiphers    []string
		wantGroups     []string
	}{
		{
			name:           "nil profile defaults to Intermediate, gate disabled",
			profile:        nil,
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     nil,
		},
		{
			name:           "nil profile defaults to Intermediate, gate enabled",
			profile:        nil,
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
		{
			name:           "nil feature gates omits groups even for Intermediate",
			profile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			featureGates:   nil,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     nil,
		},
		{
			name:           "empty profile type defaults to Intermediate, gate enabled",
			profile:        &configv1.TLSSecurityProfile{},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
		{
			name: "Intermediate profile, gate disabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     nil,
		},
		{
			name: "Intermediate profile, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
		{
			name: "Modern profile, gate disabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    ianaCiphers(configv1.TLSProfileModernType),
			wantGroups:     nil,
		},
		{
			name: "Modern profile, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    ianaCiphers(configv1.TLSProfileModernType),
			wantGroups:     profileGroups(configv1.TLSProfileModernType),
		},
		{
			name: "Old profile, gate disabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS10),
			wantCiphers:    ianaCiphers(configv1.TLSProfileOldType),
			wantGroups:     nil,
		},
		{
			name: "Old profile, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS10),
			wantCiphers:    ianaCiphers(configv1.TLSProfileOldType),
			wantGroups:     profileGroups(configv1.TLSProfileOldType),
		},
		{
			name: "unknown profile type falls back to Intermediate, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: "SomeUnknownProfile",
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
		{
			name: "Custom profile without groups, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS13,
						Ciphers:       customCiphers,
					},
				},
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS13),
			wantCiphers:    crypto.OpenSSLToIANACipherSuites(customCiphers),
			wantGroups:     []string{},
		},
		{
			name: "Custom profile with groups, gate disabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       customCiphers,
						Groups:        customGroups,
					},
				},
			},
			featureGates:   fgDisabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    crypto.OpenSSLToIANACipherSuites(customCiphers),
			wantGroups:     nil,
		},
		{
			name: "Custom profile with groups, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       customCiphers,
						Groups:        customGroups,
					},
				},
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    crypto.OpenSSLToIANACipherSuites(customCiphers),
			wantGroups:     []string{string(configv1.TLSGroupX25519), string(configv1.TLSGroupSecP256r1)},
		},
		{
			name: "Custom type with nil Custom falls back to Intermediate, gate enabled",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			featureGates:   fgEnabled,
			wantMinVersion: string(configv1.VersionTLS12),
			wantCiphers:    ianaCiphers(configv1.TLSProfileIntermediateType),
			wantGroups:     profileGroups(configv1.TLSProfileIntermediateType),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMinVersion, gotCiphers, gotGroups := TLSSettingsFromProfile(tt.profile, tt.featureGates)
			assert.Equal(t, tt.wantMinVersion, gotMinVersion)
			assert.Equal(t, tt.wantCiphers, gotCiphers)
			if tt.wantGroups == nil {
				assert.Nil(t, gotGroups)
			} else {
				assert.Equal(t, tt.wantGroups, gotGroups)
			}
		})
	}
}

func TestOperatorConfigYAMLCurvePreferences(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   []int32
	}{
		{name: "no groups", groups: nil, want: nil},
		{
			name:   "known groups become curve IDs",
			groups: []string{"X25519MLKEM768", "secp256r1"},
			want:   []int32{int32(tls.X25519MLKEM768), int32(tls.CurveP256)},
		},
		{name: "unrecognized groups dropped", groups: []string{"bogus"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			y, err := OperatorConfigYAML("VersionTLS12", []string{"A"}, tt.groups)
			assert.NoError(t, err)
			var cfg operatorv1alpha1.GenericOperatorConfig
			assert.NoError(t, sigsyaml.UnmarshalStrict([]byte(y), &cfg))
			assert.Equal(t, tt.want, cfg.ServingInfo.CurvePreferences)
		})
	}
}
