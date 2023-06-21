package vsphereproblemdetector

import (
	"reflect"
	"testing"
	"time"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func getCM(data map[string]string) *v1.ConfigMap {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      detectorConfigMapName,
			Namespace: csoclients.OperatorNamespace,
		},
		Data: data,
	}
	return cm
}

func TestParseConfigMap(t *testing.T) {
	tests := []struct {
		name           string
		configMap      *v1.ConfigMap
		expectedConfig *DetectorConfig
		expectError    bool
	}{
		{
			name:           "non-existing ConfigMap",
			configMap:      nil,
			expectedConfig: &DetectorConfig{AlertsDisabled: false},
			expectError:    false,
		},
		{
			name:           "ConfigMap with empty data",
			configMap:      getCM(map[string]string{}),
			expectedConfig: nil,
			expectError:    true,
		},
		{
			name:           "ConfigMap with invalid key",
			configMap:      getCM(map[string]string{"foo": "bar"}),
			expectedConfig: nil,
			expectError:    true,
		},
		{
			name:           "ConfigMap with empty config yaml",
			configMap:      getCM(map[string]string{configKey: ""}),
			expectedConfig: &DetectorConfig{AlertsDisabled: false},
			expectError:    false,
		},
		{
			name:           "ConfigMap with valid config yaml, explicitly disabled",
			configMap:      getCM(map[string]string{configKey: "alertsDisabled: true"}),
			expectedConfig: &DetectorConfig{AlertsDisabled: true},
			expectError:    false,
		},
		{
			name:           "ConfigMap with valid config yaml, explicitly enabled",
			configMap:      getCM(map[string]string{configKey: "alertsDisabled: false"}),
			expectedConfig: &DetectorConfig{AlertsDisabled: false},
			expectError:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var client kubernetes.Interface
			if tt.configMap != nil {
				client = fake.NewSimpleClientset(tt.configMap)
			} else {
				client = fake.NewSimpleClientset()
			}
			informerFactory := informers.NewSharedInformerFactory(client, time.Hour)
			cmInformer := informerFactory.Core().V1().ConfigMaps()
			if tt.configMap != nil {
				cmInformer.Informer().GetIndexer().Add(tt.configMap)
			}

			got, err := ParseConfigMap(cmInformer.Lister())
			if err != nil && !tt.expectError {
				t.Errorf("unexpected error: %s", err)
			}
			if err == nil && tt.expectError {
				t.Errorf("expected error, got none")
			}
			if !reflect.DeepEqual(got, tt.expectedConfig) {
				t.Errorf("unexpected config received, got = %v, want %v", got, tt.expectedConfig)
			}
		})
	}
}
