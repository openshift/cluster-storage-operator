package validation

import (
	"reflect"
	"testing"

	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCheckAlphaSnapshot(t *testing.T) {
	tests := []struct {
		name          string
		snapshotCRD   *apiextv1beta1.CustomResourceDefinition
		expectedError error
	}{
		{
			name:          "no VolumeSnapshot installed, return nil",
			snapshotCRD:   &apiextv1beta1.CustomResourceDefinition{},
			expectedError: nil,
		},
		{
			name:          "v1alpha1 VolumeSnapshot installed, return conflict error",
			snapshotCRD:   getFakeSnapshotCRD("v1alpha1"),
			expectedError: apierrors.NewConflict(schema.GroupResource{Resource: "VolumeSnapshot"}, "v1alpha1 VolumeSnapshot installed.", nil),
		},
		{
			name:          "v1beta1 VolumeSnapshot installed, return nil",
			snapshotCRD:   getFakeSnapshotCRD("v1beta1"),
			expectedError: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := apiextv1beta1.AddToScheme(scheme); err != nil {
				t.Errorf("apiextv1beta1.AddToScheme: %v", err)
			}

			client := fake.NewFakeClientWithScheme(scheme, test.snapshotCRD)
			err := CheckAlphaSnapshot(client)

			if test.expectedError != nil {
				if !reflect.DeepEqual(err, test.expectedError) {
					t.Errorf("Expected error doesn't match received error: %v \r\n %v", test.expectedError, err)
				}
			} else if err != nil {
				t.Errorf("Test expects nil error, received: %v", err)
			}
		})
	}
}

func getFakeSnapshotCRD(version string) *apiextv1beta1.CustomResourceDefinition {
	crd := &apiextv1beta1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: "apiextensions.k8s.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "volumesnapshots.snapshot.storage.k8s.io",
		},
		Spec: apiextv1beta1.CustomResourceDefinitionSpec{
			Version: version,
			Versions: []apiextv1beta1.CustomResourceDefinitionVersion{
				{
					Name:    version,
					Served:  true,
					Storage: true,
				},
			},
		},
	}
	return crd
}
