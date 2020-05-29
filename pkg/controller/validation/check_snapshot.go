package validation

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("check_snapshot")

const (
	snapshotCRDVersion = "v1alpha1"
	snapshotCRDName    = "volumesnapshots.snapshot.storage.k8s.io"
)

// Returns an error if the v1alpha1 VolumeSnapshot CRD is installed in the cluster
// and nil otherwise. This is used to prevent upgrading where clusters have manually
// installed versions we don't support
func CheckAlphaSnapshot(client client.Client) error {
	snapshotCRD := &apiextv1beta1.CustomResourceDefinition{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: snapshotCRDName, Namespace: corev1.NamespaceAll}, snapshotCRD)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If we can't find the VolumeSnapshot CRD, then we're safe to proceed
			return nil
		}
		log.Error(err, "Error attempting to obtain existing VolumeSnapshot CRD")
		return err
	}

	for _, version := range snapshotCRD.Spec.Versions {
		if version.Name == snapshotCRDVersion {
			err = apierrors.NewConflict(schema.GroupResource{Resource: "VolumeSnapshot"},
				"v1alpha1 VolumeSnapshot installed.", nil)
			log.Error(err, "Unable to update cluster as VolumeSnapshot v1alpha1 is detected. Remove this version to allow the upgrade to proceed.")
			return err
		}
	}
	return nil
}
