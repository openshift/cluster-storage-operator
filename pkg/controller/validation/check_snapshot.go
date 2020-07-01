package validation

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("check_snapshot")

const (
	alphaVersion           = "v1alpha1"
	snapshotCRDName        = "volumesnapshots.snapshot.storage.k8s.io"
	snapshotClassCRDName   = "volumesnapshotclasses.snapshot.storage.k8s.io"
	snapshotContentCRDName = "volumesnapshotcontents.snapshot.storage.k8s.io"
)

type AlphaVersionError struct {
	Msg string
}

func (e *AlphaVersionError) Error() string { return e.Msg }

// Returns an error if the v1alpha1 CRD is installed in the cluster
// and nil otherwise. This is used to prevent upgrading where clusters have manually
// installed versions we don't support

func CheckAlphaSnapshot(client client.Client) error {
	crdMap := map[string]string{"VolumeSnapshot": snapshotCRDName,
		"VolumeSnapshotClass":   snapshotClassCRDName,
		"VolumeSnapshotContent": snapshotContentCRDName}
	foundCRD := []string{}
	crd := &apiextv1beta1.CustomResourceDefinition{}
	for k, v := range crdMap {
		err := client.Get(context.TODO(), types.NamespacedName{Name: v, Namespace: corev1.NamespaceAll}, crd)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				errMsg := "Error attempting to obtain existing " + k + " CRD."
				log.Error(err, errMsg)
				return err
			}
			log.V(5).Info("CRD for " + k + " not found.")
			continue
		}

		for _, version := range crd.Spec.Versions {
			if version.Name == alphaVersion {
				foundCRD = append(foundCRD, v)
				log.Error(err, "v1alpha1 "+k+" installed")
			}
		}
	}
	if len(foundCRD) > 0 {
		errString := "Unable to update cluster as v1alpha1 version of " + strings.Join(foundCRD, ", ") + "is detected. Remove these CRDs to allow the upgrade to proceed."
		return &AlphaVersionError{errString}
	}
	return nil
}
