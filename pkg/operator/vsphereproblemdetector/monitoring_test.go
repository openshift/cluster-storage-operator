package vsphereproblemdetector

import (
	"context"
	"testing"
	"time"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/events"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestSyncPrometheusRule(t *testing.T) {
	tests := []struct {
		name           string
		inititialRules []*promv1.PrometheusRule
		// we merely use this field as a marker in test to check if rule was applied properly
		expectedAlertCountInRule int
		modified                 bool
	}{
		{
			name:                     "for new rule creation",
			inititialRules:           []*promv1.PrometheusRule{},
			expectedAlertCountInRule: 2,
			modified:                 true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialObjects := &csoclients.FakeTestObjects{}
			for _, r := range test.inititialRules {
				initialObjects.MonitoringObjects = append(initialObjects.MonitoringObjects, runtime.Object(r))
			}

			client := csoclients.NewFakeClients(initialObjects)
			eventRecorder := events.NewInMemoryRecorder("vsphere-client", clocktesting.NewFakePassiveClock(time.Now()))
			c := &monitoringController{
				operatorClient:   client.OperatorClient,
				kubeClient:       client.KubeClient,
				dynamicClient:    client.DynamicClient,
				eventRecorder:    eventRecorder,
				monitoringClient: client.MonitoringClient,
			}
			ctx := context.TODO()
			rule, modified, err := c.syncPrometheusRule(ctx, getPrometheusRuleRaw())
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if modified != test.modified {
				t.Errorf("expected rule modification to be %v got %v", test.modified, modified)
			}
			actualRules := rule.Spec.Groups[0].Rules
			if len(actualRules) != test.expectedAlertCountInRule {
				t.Errorf("expected alert count in rule to be %d got %d", test.expectedAlertCountInRule, len(actualRules))
			}
		})

	}
}

func getPrometheusRuleRaw() []byte {
	return []byte(`
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: vsphere-problem-detector
  namespace: openshift-cluster-storage-operator
  labels:
    role: alert-rules
spec:
  groups:
    - name: vsphere-problem-detector.rules
      rules:
      - alert: VSphereOpenshiftNodeHealthFail
        expr:  vsphere_node_check_errors == 1
        for: 10m
        labels:
          severity: warning
        annotations:
          message: "Vsphere node health checks are failing on {{ $labels.node }} with {{ $labels.check }}"
      - alert: VSphereOpenshiftClusterHealthFail
        expr: vsphere_cluster_check_errors == 1
        for: 10m
        labels:
          severity: critical
        annotations:
          message: "VSpehre cluster health checks are failing with {{ $labels.check }}"
         `)
}
