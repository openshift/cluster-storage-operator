package selinuxmountreadiness

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	cfgv1 "github.com/openshift/api/config/v1"
	opv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
)

type testContext struct {
	controller factory.Controller
	clients    *csoclients.Clients
}

type operatorTest struct {
	name            string
	featureGates    featuregates.FeatureGate
	initialObjects  testObjects
	expectedObjects testObjects
	expectErr       bool
}

type testObjects struct {
	storage   *opv1.Storage
	configMap *corev1.ConfigMap
}

func newController(test operatorTest) *testContext {
	initialObjects := &csoclients.FakeTestObjects{}
	if test.initialObjects.storage != nil {
		initialObjects.OperatorObjects = []runtime.Object{test.initialObjects.storage}
	}
	if test.initialObjects.configMap != nil {
		initialObjects.CoreObjects = []runtime.Object{test.initialObjects.configMap}
	}

	clients := csoclients.NewFakeClients(initialObjects)
	recorder := events.NewInMemoryRecorder("operator", clocktesting.NewFakePassiveClock(time.Now()))

	fg := test.featureGates
	if fg == nil {
		fg = featuregates.NewFeatureGate([]cfgv1.FeatureGateName{SELinuxMountGAReadinessFeatureGate}, nil)
	}

	ctrl := NewController(clients, fg, recorder)
	return &testContext{
		controller: ctrl,
		clients:    clients,
	}
}

func selinuxConflictsConfigMap(conflictsPresent string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      selinuxConflictsConfigMapName,
			Namespace: csoclients.CloudConfigNamespace,
		},
		Data: map[string]string{
			selinuxConflictsDataKey: conflictsPresent,
		},
	}
}

func withUpgradeableCondition(status opv1.ConditionStatus, reason, message string) csoclients.CrModifier {
	return func(i *opv1.Storage) *opv1.Storage {
		if i.Status.Conditions == nil {
			i.Status.Conditions = []opv1.OperatorCondition{}
		}
		i.Status.Conditions = append(i.Status.Conditions, opv1.OperatorCondition{
			Type:    conditionsPrefix + opv1.OperatorStatusTypeUpgradeable,
			Status:  status,
			Reason:  reason,
			Message: message,
		})
		return i
	}
}

func TestSync(t *testing.T) {
	tests := []operatorTest{
		{
			name: "feature gate disabled sets upgradeable true",
			initialObjects: testObjects{
				storage:   csoclients.GetCR(),
				configMap: selinuxConflictsConfigMap(string(metav1.ConditionTrue)),
			},
			featureGates: featuregates.NewFeatureGate(nil, []cfgv1.FeatureGateName{SELinuxMountGAReadinessFeatureGate}),
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withUpgradeableCondition(opv1.ConditionTrue, "", ""),
				),
			},
		},
		{
			name: "missing config map sets upgradeable true",
			initialObjects: testObjects{
				storage: csoclients.GetCR(),
			},
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withUpgradeableCondition(opv1.ConditionTrue, "", ""),
				),
			},
		},
		{
			name: "no conflicts sets upgradeable true",
			initialObjects: testObjects{
				storage:   csoclients.GetCR(),
				configMap: selinuxConflictsConfigMap(string(metav1.ConditionFalse)),
			},
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withUpgradeableCondition(opv1.ConditionTrue, "", ""),
				),
			},
		},
		{
			name: "conflicts present sets upgradeable false",
			initialObjects: testObjects{
				storage:   csoclients.GetCR(),
				configMap: selinuxConflictsConfigMap(string(metav1.ConditionTrue)),
			},
			expectedObjects: testObjects{
				storage: csoclients.GetCR(
					withUpgradeableCondition(opv1.ConditionFalse, "SELinuxMountIncompatibleWorkloads", upgradeBlockedMessage()),
				),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := newController(test)
			stopCh := make(chan struct{})
			csoclients.StartInformers(ctx.clients, stopCh)
			csoclients.WaitForSync(ctx.clients, stopCh)
			defer close(stopCh)

			err := ctx.controller.Sync(context.TODO(), nil)
			if err != nil && !test.expectErr {
				t.Fatalf("sync() returned unexpected error: %v", err)
			}
			if err == nil && test.expectErr {
				t.Fatal("sync() unexpectedly succeeded when error was expected")
			}

			_, status, _, err := ctx.clients.OperatorClient.GetOperatorState()
			if err != nil {
				t.Fatalf("failed to get Storage: %v", err)
			}
			sanitizeStatus(status)
			sanitizeStatus(&test.expectedObjects.storage.Status.OperatorStatus)
			if !equality.Semantic.DeepEqual(test.expectedObjects.storage.Status.OperatorStatus, *status) {
				t.Fatalf("unexpected Storage status:\n%s", cmp.Diff(test.expectedObjects.storage.Status.OperatorStatus, *status))
			}
		})
	}
}

func TestConflictsPresent(t *testing.T) {
	tests := []struct {
		name           string
		configMap      *corev1.ConfigMap
		wantPresent    bool
		wantFound      bool
	}{
		{
			name:        "nil config map",
			configMap:   nil,
			wantPresent: false,
			wantFound:   false,
		},
		{
			name:        "missing data key",
			configMap:   &corev1.ConfigMap{Data: map[string]string{}},
			wantPresent: false,
			wantFound:   true,
		},
		{
			name:        "conflicts false",
			configMap:   selinuxConflictsConfigMap(string(metav1.ConditionFalse)),
			wantPresent: false,
			wantFound:   true,
		},
		{
			name:        "conflicts true",
			configMap:   selinuxConflictsConfigMap(string(metav1.ConditionTrue)),
			wantPresent: true,
			wantFound:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPresent, gotFound := ConflictsPresent(tt.configMap)
			if gotPresent != tt.wantPresent || gotFound != tt.wantFound {
				t.Fatalf("ConflictsPresent() = (%v, %v), want (%v, %v)", gotPresent, gotFound, tt.wantPresent, tt.wantFound)
			}
		})
	}
}

func sanitizeStatus(status *opv1.OperatorStatus) {
	for i := range status.Conditions {
		status.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	sort.Slice(status.Conditions, func(i, j int) bool {
		return status.Conditions[i].Type < status.Conditions[j].Type
	})
}
