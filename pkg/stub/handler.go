package stub

import (
	"context"
	"encoding/json"

	"github.com/openshift/cluster-storage-operator/pkg/apis/storage/v1alpha1"

	"github.com/openshift/installer/pkg/types"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	//"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func NewHandler() sdk.Handler {
	return &Handler{}
}

type Handler struct {
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.ClusterStorage:
		return h.sync(o)
	}
	return nil
}

func (h *Handler) sync(_ *v1alpha1.ClusterStorage) error {
	platform, err := getPlatform()
	if err != nil {
		return err
	}
	if platform.AWS != nil {
		h.syncAWS(*platform.AWS)
	} else if platform.Libvirt != nil {
	} else if platform.OpenStack != nil {
	}

	return nil
}

func (h *Handler) syncAWS(types.AWSPlatform) error {

	// TODO create default ebs storage class
	// TODO efs operator installation (create subscription)
	return nil
}

func getPlatform() (*types.Platform, error) {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
	}
	err := sdk.Get(cm)
	if err != nil {
		return nil, err
	}

	data, err := utilyaml.ToJSON([]byte(cm.Data["install-config"]))
	if err != nil {
		return nil, err
	}

	config := &types.InstallConfig{}
	json.Unmarshal(data, &config)

	return &config.Platform, nil
}
