package clusterstorage

import (
	"context"
	"os"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/apis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSetStatusProgressing(t *testing.T) {
	version1 := "1"
	version2 := "2"
	tests := []struct {
		name              string
		releaseVersion    string
		operatorVersion   *string
		expectProgressing bool
	}{
		{
			name:              "operator version matches, do nothing",
			releaseVersion:    version2,
			operatorVersion:   &version2,
			expectProgressing: false,
		},
		{
			name:              "operator version doesn't match, progressing",
			releaseVersion:    version2,
			operatorVersion:   &version1,
			expectProgressing: true,
		},
		{
			name:              "operator version nil, progressing",
			releaseVersion:    version2,
			operatorVersion:   nil,
			expectProgressing: true,
		},
		{
			name:              "release version empty, progressing",
			releaseVersion:    "",
			operatorVersion:   nil,
			expectProgressing: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			os.Setenv("RELEASE_VERSION", test.releaseVersion)

			clusterOperator := &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "storage",
					Namespace: corev1.NamespaceAll,
				},
			}
			if test.operatorVersion != nil {
				clusterOperator.Status.Versions = []configv1.OperandVersion{{Name: "operator", Version: *test.operatorVersion}}
			}

			scheme := runtime.NewScheme()
			if err := apis.AddToScheme(scheme); err != nil {
				t.Errorf("apis.AddToScheme: %v", err)
			}
			if err := configv1.AddToScheme(scheme); err != nil {
				t.Errorf("configv1.AddToScheme: %v", err)
			}
			client := fake.NewFakeClientWithScheme(scheme, clusterOperator)
			reconciler := &ReconcileClusterStorage{client: client, scheme: scheme}

			if err := reconciler.setStatusProgressing(clusterOperator); err != nil {
				t.Errorf("setStatusProgressing: %v", err)
			}

			if err := client.Get(context.TODO(), types.NamespacedName{Name: "storage", Namespace: corev1.NamespaceAll}, clusterOperator); err != nil {
				t.Errorf("Get: %v", err)
			}
			progressing := false
			for _, c := range clusterOperator.Status.Conditions {
				if c.Type == configv1.OperatorProgressing && c.Status == configv1.ConditionTrue {
					progressing = true
					break
				}
			}
			if test.expectProgressing != progressing {
				t.Errorf("expected progressing %v but got %v", test.expectProgressing, progressing)
			}
		})
	}
}
