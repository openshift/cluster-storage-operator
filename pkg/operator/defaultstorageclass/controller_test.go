package defaultstorageclass

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	cfgv1 "github.com/openshift/api/config/v1"
	opv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type testContext struct {
	controller factory.Controller
	clients    *csoclients.Clients
}

type testObjects struct {
	storage        *opv1.Storage
	infrastructure *cfgv1.Infrastructure
	storageClasses []*storagev1.StorageClass
}

type operatorTest struct {
	name            string
	initialObjects  testObjects
	expectedObjects testObjects
	expectErr       bool
}

func newController(test operatorTest) *testContext {
	// Convert to []runtime.Object
	initialObjects := &csoclients.FakeTestObjects{}
	for _, c := range test.initialObjects.storageClasses {
		initialObjects.CoreObjects = append(initialObjects.CoreObjects, c)
	}
	if test.initialObjects.storage != nil {
		initialObjects.OperatorObjects = []runtime.Object{test.initialObjects.storage}
	}
	if test.initialObjects.infrastructure != nil {
		initialObjects.ConfigObjects = []runtime.Object{test.initialObjects.infrastructure}
	}

	clients := csoclients.NewFakeClients(initialObjects)

	recorder := events.NewInMemoryRecorder("operator")
	ctrl := NewController(clients, recorder)

	return &testContext{
		controller: ctrl,
		clients:    clients,
	}
}

type storageClassModifier func(class *storagev1.StorageClass) *storagev1.StorageClass

func getPlatformStorageClass(filename string, modifiers ...storageClassModifier) *storagev1.StorageClass {
	sc := resourceread.ReadStorageClassV1OrDie(generated.MustAsset(filename))
	for _, modifier := range modifiers {
		sc = modifier(sc)
	}
	return sc
}

func withNoDefault(class *storagev1.StorageClass) *storagev1.StorageClass {
	class.Annotations = nil
	return class
}

func getInfrastructure(platformType cfgv1.PlatformType) *cfgv1.Infrastructure {
	return &cfgv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: infraConfigName,
		},
		Status: cfgv1.InfrastructureStatus{
			PlatformStatus: &cfgv1.PlatformStatus{
				Type: platformType,
			},
		},
	}
}

type crModifier func(cr *opv1.Storage) *opv1.Storage

func getCR(modifiers ...crModifier) *opv1.Storage {
	cr := &opv1.Storage{
		ObjectMeta: metav1.ObjectMeta{Name: operatorclient.GlobalConfigName},
		Spec: opv1.StorageSpec{
			OperatorSpec: opv1.OperatorSpec{
				ManagementState: opv1.Managed,
			},
		},
		Status: opv1.StorageStatus{},
	}
	for _, modifier := range modifiers {
		cr = modifier(cr)
	}
	return cr
}

func withTrueConditions(conditions ...string) crModifier {
	return func(i *opv1.Storage) *opv1.Storage {
		if i.Status.Conditions == nil {
			i.Status.Conditions = []opv1.OperatorCondition{}
		}
		for _, c := range conditions {
			i.Status.Conditions = append(i.Status.Conditions, opv1.OperatorCondition{
				Type:   c,
				Status: opv1.ConditionTrue,
			})
		}
		return i
	}
}

func withFalseConditions(conditions ...string) crModifier {
	return func(i *opv1.Storage) *opv1.Storage {
		if i.Status.Conditions == nil {
			i.Status.Conditions = []opv1.OperatorCondition{}
		}
		for _, c := range conditions {
			i.Status.Conditions = append(i.Status.Conditions, opv1.OperatorCondition{
				Type:   c,
				Status: opv1.ConditionFalse,
			})
		}
		return i
	}
}

func TestSync(t *testing.T) {
	tests := []operatorTest{
		{
			// Default storage class is deployed if it does not exist
			name: "initial AWS deployment",
			initialObjects: testObjects{
				storage:        getCR(),
				infrastructure: getInfrastructure(cfgv1.AWSPlatformType),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(ConditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(ConditionsPrefix+opv1.OperatorStatusTypeProgressing),
				),
				storageClasses: []*storagev1.StorageClass{getPlatformStorageClass("storageclasses/aws.yaml")},
			},
			expectErr: false,
		},
		{
			// The controller reports Disable on unsupported platforms
			name: "initial unsupported platform deployment",
			initialObjects: testObjects{
				storage:        getCR(),
				infrastructure: getInfrastructure(cfgv1.BareMetalPlatformType),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(ConditionsPrefix+"Disabled", ConditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(ConditionsPrefix+opv1.OperatorStatusTypeProgressing),
				),
			},
			expectErr: false,
		},
		{
			// Everything is deployed and the controller does nothing
			name: "everything deployed",
			initialObjects: testObjects{
				storage: getCR(
					withTrueConditions(ConditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(ConditionsPrefix+opv1.OperatorStatusTypeProgressing),
				),
				storageClasses: []*storagev1.StorageClass{getPlatformStorageClass("storageclasses/aws.yaml")},
				infrastructure: getInfrastructure(cfgv1.AWSPlatformType),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(ConditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(ConditionsPrefix+opv1.OperatorStatusTypeProgressing),
				),
				storageClasses: []*storagev1.StorageClass{getPlatformStorageClass("storageclasses/aws.yaml")},
			},
			expectErr: false,
		},
		{
			// The controller does not set default storage class when removed by user
			name: "default storage class removed by user",
			initialObjects: testObjects{
				storage:        getCR(),
				storageClasses: []*storagev1.StorageClass{getPlatformStorageClass("storageclasses/aws.yaml", withNoDefault)},
				infrastructure: getInfrastructure(cfgv1.AWSPlatformType),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(ConditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(ConditionsPrefix+opv1.OperatorStatusTypeProgressing),
				),
				storageClasses: []*storagev1.StorageClass{getPlatformStorageClass("storageclasses/aws.yaml", withNoDefault)},
			},
			expectErr: false,
		},
		{
			// The controller returns error
			name: "infrastructure not found",
			initialObjects: testObjects{
				storage: getCR(),
			},
			expectedObjects: testObjects{
				storage: getCR(
					withTrueConditions(ConditionsPrefix+opv1.OperatorStatusTypeProgressing),
					withFalseConditions(ConditionsPrefix+opv1.OperatorStatusTypeAvailable),
				),
			},
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Initialize
			ctx := newController(test)
			finish, cancel := context.WithCancel(context.TODO())
			defer cancel()
			csoclients.StartInformers(ctx.clients, finish.Done())
			csoclients.WaitForSync(ctx.clients, finish.Done())

			// Act
			err := ctx.controller.Sync(context.TODO(), nil)

			// Assert
			// Check error
			if err != nil && !test.expectErr {
				t.Errorf("sync() returned unexpected error: %v", err)
			}
			if err == nil && test.expectErr {
				t.Error("sync() unexpectedly succeeded when error was expected")
			}

			// Check expectedObjects.storage
			if test.expectedObjects.storage != nil {
				actualStorage, err := ctx.clients.OperatorClientSet.OperatorV1().Storages().Get(context.TODO(), "cluster", metav1.GetOptions{})
				if err != nil {
					t.Errorf("Failed to get Storage: %v", err)
				}
				sanitizeStorage(actualStorage)
				sanitizeStorage(test.expectedObjects.storage)
				if !equality.Semantic.DeepEqual(test.expectedObjects.storage, actualStorage) {
					t.Errorf("Unexpected Storage content:\n%s", cmp.Diff(test.expectedObjects.storage, actualStorage))
				}
			}
			// Check expectedObjects.storageClasses
			actualSCList, _ := ctx.clients.KubeClient.StorageV1().StorageClasses().List(context.TODO(), metav1.ListOptions{})
			actualSCs := map[string]*storagev1.StorageClass{}
			for i := range actualSCList.Items {
				sc := &actualSCList.Items[i]
				actualSCs[sc.Name] = sc
			}
			expectedSCs := map[string]*storagev1.StorageClass{}
			for _, sc := range test.expectedObjects.storageClasses {
				expectedSCs[sc.Name] = sc
			}

			for name, actualSC := range actualSCs {
				expectedSC, found := expectedSCs[name]
				if !found {
					t.Errorf("Unexpected StorageClass found: %s", name)
					continue
				}
				if !equality.Semantic.DeepEqual(expectedSC, actualSC) {
					t.Errorf("Unexpected StorageClass %+v content:\n%s", name, cmp.Diff(expectedSC, actualSC))
				}
				delete(expectedSCs, name)
			}
			if len(expectedSCs) > 0 {
				for _, crd := range expectedSCs {
					t.Errorf("StorageClass %s not created by Sync()", crd.Name)
				}
			}
		})
	}
}

func sanitizeStorage(instance *opv1.Storage) {
	// Remove condition texts
	for i := range instance.Status.Conditions {
		instance.Status.Conditions[i].LastTransitionTime = metav1.Time{}
		instance.Status.Conditions[i].Message = ""
		instance.Status.Conditions[i].Reason = ""
	}
	// Sort the conditions by name to have consistent position in the array
	sort.Slice(instance.Status.Conditions, func(i, j int) bool {
		return instance.Status.Conditions[i].Type < instance.Status.Conditions[j].Type
	})
}
