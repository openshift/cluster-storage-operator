package defaultstorageclass

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	cfgv1 "github.com/openshift/api/config/v1"
	opv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
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

	recorder := events.NewInMemoryRecorder("operator", clocktesting.NewFakePassiveClock(time.Now()))
	ctrl := NewController(clients, recorder)

	return &testContext{
		controller: ctrl,
		clients:    clients,
	}
}

type storageClassModifier func(class *storagev1.StorageClass) *storagev1.StorageClass

func getPlatformStorageClass(filename string, modifiers ...storageClassModifier) *storagev1.StorageClass {
	assetBytes, err := assets.ReadFile(filename)
	if err != nil {
		panic(err)
	}
	sc := resourceread.ReadStorageClassV1OrDie(assetBytes)
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

func getAzureStackHubInfrastructure() *cfgv1.Infrastructure {
	return &cfgv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: infraConfigName,
		},
		Status: cfgv1.InfrastructureStatus{
			PlatformStatus: &cfgv1.PlatformStatus{
				Type: cfgv1.AzurePlatformType,
				Azure: &cfgv1.AzurePlatformStatus{
					CloudName: cfgv1.AzureStackCloud,
				},
			},
		},
	}
}

func withTrueConditions(conditions ...string) csoclients.CrModifier {
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

func withFalseConditions(conditions ...string) csoclients.CrModifier {
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
			// The controller reports Disable on unsupported platforms
			name: "initial unsupported platform deployment",
			initialObjects: testObjects{
				storage:        csoclients.GetCR(),
				infrastructure: getInfrastructure(cfgv1.BareMetalPlatformType),
			},
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withTrueConditions(conditionsPrefix+"Disabled", conditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(conditionsPrefix+opv1.OperatorStatusTypeProgressing),
					withTrueConditions(conditionsPrefix+opv1.OperatorStatusTypeUpgradeable),
				),
			},
			expectErr: false,
		},
		{
			// The controller returns error - missing Available is added
			name: "infrastructure not found",
			initialObjects: testObjects{
				storage: csoclients.GetCR(),
			},
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withTrueConditions(conditionsPrefix+opv1.OperatorStatusTypeProgressing),
					withFalseConditions(conditionsPrefix+opv1.OperatorStatusTypeAvailable),
				),
			},
			expectErr: true,
		},
		{
			// The controller returns error + Available is True -> not flipped to False
			name: "available not false after error",
			initialObjects: testObjects{
				storage: csoclients.GetCR(
					withTrueConditions(conditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withFalseConditions(conditionsPrefix+opv1.OperatorStatusTypeProgressing),
				),
			},
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withTrueConditions(conditionsPrefix+opv1.OperatorStatusTypeAvailable),
					withTrueConditions(conditionsPrefix+opv1.OperatorStatusTypeProgressing),
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
				_, status, _, err := ctx.clients.OperatorClient.GetOperatorState()
				if err != nil {
					t.Errorf("Failed to get Storage: %v", err)
				}
				sanitizeStatus(status)
				sanitizeStatus(&test.expectedObjects.storage.Status.OperatorStatus)
				if !equality.Semantic.DeepEqual(test.expectedObjects.storage.Status.OperatorStatus, *status) {
					t.Errorf("Unexpected Storage content:\n%s", cmp.Diff(test.expectedObjects.storage.Status.OperatorStatus, *status))
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

func sanitizeStatus(status *opv1.OperatorStatus) {
	// Remove condition texts
	for i := range status.Conditions {
		status.Conditions[i].LastTransitionTime = metav1.Time{}
		status.Conditions[i].Message = ""
		status.Conditions[i].Reason = ""
	}
	// Sort the conditions by name to have consistent position in the array
	sort.Slice(status.Conditions, func(i, j int) bool {
		return status.Conditions[i].Type < status.Conditions[j].Type
	})
}
