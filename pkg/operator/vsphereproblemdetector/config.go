package vsphereproblemdetector

import (
	"fmt"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/errors"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

type DetectorConfig struct {
	AlertsDisabled bool `yaml:"alertsDisabled,omitempty"`
}

var (
	defaultConfig = DetectorConfig{
		// Alerts are enabled by default
		AlertsDisabled: false,
	}
)

const (
	detectorConfigMapName = "vsphere-problem-detector"
	configKey             = "config.yaml"
)

func ParseConfigMap(lister listerv1.ConfigMapLister) (*DetectorConfig, error) {
	cm, err := lister.ConfigMaps(csoclients.OperatorNamespace).Get(detectorConfigMapName)
	if err != nil {
		if errors.IsNotFound(err) {
			// Missing ConfigMap indicates default config
			klog.V(4).Infof("Using default config, %s does not exist", detectorConfigMapName)
			return &defaultConfig, nil
		}
		return nil, err
	}

	data, found := cm.Data[configKey]
	if !found {
		return nil, fmt.Errorf("invalid format of ConfigMap %s: expected key %s", detectorConfigMapName, configKey)
	}

	config := defaultConfig
	err = yaml.UnmarshalStrict([]byte(data), &config)
	if err != nil {
		return nil, fmt.Errorf("invalid format of ConfigMap %s: %s", detectorConfigMapName, err)
	}
	klog.V(4).Infof("Parsed ConfigMap %s: %+v", detectorConfigMapName, config)
	return &config, nil
}
