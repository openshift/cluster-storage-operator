package validation

import (
	"testing"

	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCheckAlphaSnapshot(t *testing.T) {
	tests := []struct {
		name          string
		snapshotCRD   *apiextv1beta1.CustomResourceDefinition
		expectedError string
	}{
		{
			name:          "no VolumeSnapshot installed, return nil",
			snapshotCRD:   &apiextv1beta1.CustomResourceDefinition{},
			expectedError: "",
		},
		{
			name:          "v1alpha1 VolumeSnapshot installed, return conflict error",
			snapshotCRD:   getFakeSnapshotCRD("v1alpha1"),
			expectedError: "conflict",
		},
		{
			name:          "v1beta1 VolumeSnapshot installed, return nil",
			snapshotCRD:   getFakeSnapshotCRD("v1beta1"),
			expectedError: "",
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

			if test.expectedError != "" {
				if err, ok := err.(*AlphaVersionError); !ok {
					t.Errorf("expected AlphaVersionError error, received: %v", err)
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
