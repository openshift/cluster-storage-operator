package clusterstorage

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/util/diff"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/apis"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	storageClassName = "gp2"
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

func TestReconcile(t *testing.T) {

	platform := configv1.AWSPlatformType

	tests := []struct {
		name                 string
		existingStorageClass *storagev1.StorageClass
		expectedStorageClass *storagev1.StorageClass
		oldNamespaceExists   bool
	}{
		{
			name:                 "StorageClass needs to be updated. Annotations must be kept intact.",
			existingStorageClass: getFakeStorageClass(false, false, ""),
			expectedStorageClass: getFakeStorageClass(true, false, "kubernetes.io/aws-ebs"),
		},
		{
			name:                 "StorageClass matches and does not need to be updated",
			existingStorageClass: getFakeStorageClass(true, true, "kubernetes.io/aws-ebs"),
			expectedStorageClass: getFakeStorageClass(true, true, "kubernetes.io/aws-ebs"),
		},
		{
			name:                 "StorageClass does not exist",
			existingStorageClass: nil,
			expectedStorageClass: getFakeStorageClass(true, true, "kubernetes.io/aws-ebs"),
		},
		{
			name:                 "Old namespace must be deleted.",
			existingStorageClass: nil,
			expectedStorageClass: getFakeStorageClass(true, true, "kubernetes.io/aws-ebs"),
			oldNamespaceExists:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clusterOperator := &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "storage",
					Namespace: corev1.NamespaceAll,
				},
			}
			infrastructure := &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "storage",
					Namespace: corev1.NamespaceAll,
				},
				Status: configv1.InfrastructureStatus{
					Platform: platform,
				},
			}
			scheme := runtime.NewScheme()
			if err := apis.AddToScheme(scheme); err != nil {
				t.Errorf("apis.AddToScheme: %v", err)
			}
			if err := configv1.AddToScheme(scheme); err != nil {
				t.Errorf("configv1.AddToScheme: %v", err)
			}
			if err := storagev1.AddToScheme(scheme); err != nil {
				t.Errorf("storagev1.AddToScheme: %v", err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Errorf("corev1.AddToScheme: %v", err)
			}

			objs := []runtime.Object{clusterOperator, infrastructure}
			if test.existingStorageClass != nil {
				objs = append(objs, test.existingStorageClass)
			}

			if test.oldNamespaceExists {
				ns := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: oldClusterOperatorNamespace,
					},
				}
				objs = append(objs, ns)
			}

			client := fake.NewFakeClientWithScheme(scheme, objs...)
			reconciler := &ReconcileClusterStorage{client: client, scheme: scheme}

			request := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "storage",
					Namespace: corev1.NamespaceAll,
				},
			}
			_, err := reconciler.Reconcile(request)
			if err != nil {
				t.Errorf("Reconcile: %v", err)
			}

			sc := &storagev1.StorageClass{}
			err = client.Get(context.TODO(), types.NamespacedName{Name: storageClassName, Namespace: corev1.NamespaceAll}, sc)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
			// Manually update the OwnerReferences to match the Operator added values
			sc.OwnerReferences = test.expectedStorageClass.OwnerReferences

			if !reflect.DeepEqual(sc, test.expectedStorageClass) {
				t.Errorf("StorageClass doesn't match expected result: %v", diff.ObjectDiff(test.expectedStorageClass, sc))
			}

			ns := &corev1.Namespace{}
			err = client.Get(context.TODO(), types.NamespacedName{Name: oldClusterOperatorNamespace, Namespace: corev1.NamespaceAll}, ns)
			if err == nil {
				t.Errorf("Expected the old namespace to be deleted, but it was found")
			}
			if !apierrors.IsNotFound(err) {
				t.Errorf("Expected the old namespace to be deleted, but got err: %s", err)
			}
		})
	}
}

func getFakeStorageClass(allowVolumeExpansion, isDefaultClass bool, provisioner string) *storagev1.StorageClass {
	volumeBindingMode := storagev1.VolumeBindingWaitForFirstConsumer

	sc := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClassName,
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": strconv.FormatBool(isDefaultClass),
			},
		},
		AllowVolumeExpansion: &allowVolumeExpansion,
		VolumeBindingMode:    &volumeBindingMode,
		Parameters: map[string]string{
			"encrypted": "true",
			"type":      "gp2",
		},
	}

	if provisioner != "" {
		sc.Provisioner = provisioner
	}

	return sc
}
