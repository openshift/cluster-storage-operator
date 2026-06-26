package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	g "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	cfgclientset "github.com/openshift/client-go/config/clientset/versioned"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	selinuxReadinessConditionType = "SELinuxMountGAReadinessControllerUpgradeable"
	selinuxConflictMetric         = "selinux_warning_controller_selinux_volume_conflict"
	selinuxReadinessAlertName     = "SELinuxMountGAReadinessWorkloadsDetected"
)

var _ = g.Describe("[sig-storage][OCPFeatureGate:SELinuxMountGAReadiness] SELinux Mount Upgrade Readiness",
	g.Label("Conformance"),
	g.Label("Serial"),
	func() {
		var (
			kubeClient   *kubernetes.Clientset
			configClient cfgclientset.Interface
			opClient     opclient.Interface
			dynClient    dynamic.Interface
			kubeConfig   *rest.Config
		)

		g.BeforeEach(func() {
			var err error
			kubeConfig, err = newClientConfigForTest()
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get kubeconfig: %v", err))
			}
			agentConfig := rest.AddUserAgent(kubeConfig, clientName)
			kubeClient = kubernetes.NewForConfigOrDie(agentConfig)
			configClient = cfgclientset.NewForConfigOrDie(agentConfig)
			opClient = opclient.NewForConfigOrDie(agentConfig)
			dynClient = dynamic.NewForConfigOrDie(agentConfig)
		})

		g.It("should block upgrades when pods with conflicting SELinux labels share a volume", func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()

			g.By("Checking platform type")
			infra, err := configClient.ConfigV1().Infrastructures().Get(testContext, "cluster", metav1.GetOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get infrastructure: %v", err))
			}
			switch infra.Status.PlatformStatus.Type {
			case configv1.BareMetalPlatformType, configv1.NonePlatformType:
				g.Skip(fmt.Sprintf("Test not applicable for platform %s (no default CSI storage)", infra.Status.PlatformStatus.Type))
			}

			g.By("Creating test namespace")
			ns := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "selinux-readiness-test-",
				},
			}
			ns, err = kubeClient.CoreV1().Namespaces().Create(testContext, ns, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create namespace: %v", err))
			}
			defer func() {
				g.By(fmt.Sprintf("Cleaning up namespace %s", ns.Name))
				if err := cleanupNamespace(testContext, kubeClient, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to cleanup namespace")
				}
			}()
			g.GinkgoLogr.Info("Created namespace", "namespace", ns.Name)

			g.By("Creating RWO PVC with default StorageClass")
			pvc := &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "selinux-readiness-test-",
					Namespace:    ns.Name,
				},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
					Resources: v1.VolumeResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			pvc, err = kubeClient.CoreV1().PersistentVolumeClaims(ns.Name).Create(testContext, pvc, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create PVC: %v", err))
			}
			g.GinkgoLogr.Info("Created PVC", "pvc", pvc.Name)

			g.By("Creating pod with SELinux level s0:c0,c0")
			pod1 := newSelinuxTestPod(ns.Name, "selinux-test-pod-1", pvc.Name, "s0:c0,c0")
			pod1, err = kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod1, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod1: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod1.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod1")
				}
			}()
			g.GinkgoLogr.Info("Created pod", "pod", pod1.Name)

			g.By("Creating pod with SELinux level s0:c1,c1")
			pod2 := newSelinuxTestPod(ns.Name, "selinux-test-pod-2", pvc.Name, "s0:c1,c1")
			pod2, err = kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod2, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod2: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod2.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod2")
				}
			}()
			g.GinkgoLogr.Info("Created pod", "pod", pod2.Name)

			g.By("Waiting for SELinuxMountGAReadinessControllerUpgradeable=False")
			err = waitForOperatorCondition(testContext, opClient, selinuxReadinessConditionType, operatorapi.ConditionFalse)
			if err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for Upgradeable=False: %v", err))
			}

			g.By("Setting up Prometheus client")
			thanosURL, err := getThanosQuerierURL(testContext, dynClient)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get Thanos Querier URL: %v", err))
			}
			transport, err := rest.TransportFor(kubeConfig)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create HTTP transport: %v", err))
			}
			httpClient := &http.Client{Transport: transport}

			g.By("Verifying selinux_warning_controller_selinux_volume_conflict metric > 0")
			err = wait.PollUntilContextCancel(testContext, waitPollInterval, true, func(ctx context.Context) (bool, error) {
				value, qErr := queryPrometheusMetric(ctx, httpClient, thanosURL, selinuxConflictMetric)
				if qErr != nil {
					g.GinkgoLogr.Info("Metric query failed, retrying", "error", qErr)
					return false, nil
				}
				if value > 0 {
					g.GinkgoLogr.Info("Metric reports conflicts", "value", value)
					return true, nil
				}
				return false, nil
			})
			if err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for metric %s > 0: %v", selinuxConflictMetric, err))
			}

			g.By("Verifying SELinuxMountGAReadinessWorkloadsDetected alert is pending")
			err = wait.PollUntilContextCancel(testContext, waitPollInterval, true, func(ctx context.Context) (bool, error) {
				state, qErr := getAlertState(ctx, httpClient, thanosURL, selinuxReadinessAlertName)
				if qErr != nil {
					g.GinkgoLogr.Info("Alert query failed, retrying", "error", qErr)
					return false, nil
				}
				if state == "pending" || state == "firing" {
					g.GinkgoLogr.Info("Alert is active", "alertname", selinuxReadinessAlertName, "state", state)
					return true, nil
				}
				return false, nil
			})
			if err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for alert %s to become pending: %v", selinuxReadinessAlertName, err))
			}

			g.By(fmt.Sprintf("Deleting conflicting pod %s", pod2.Name))
			err = deletePodWithWait(testContext, kubeClient, pod2.Name, ns.Name)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to delete pod2: %v", err))
			}
			pod2.Name = ""

			g.By("Waiting for SELinuxMountGAReadinessControllerUpgradeable=True")
			err = waitForOperatorCondition(testContext, opClient, selinuxReadinessConditionType, operatorapi.ConditionTrue)
			if err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for Upgradeable=True: %v", err))
			}
		})
	})

func newSelinuxTestPod(ns, name, pvcName, selinuxLevel string) *v1.Pod {
	falseValue := false
	trueValue := true

	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    "pause",
					Image:   PauseImage,
					Command: []string{"/pause"},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "test-volume",
							MountPath: "/data",
						},
					},
					SecurityContext: &v1.SecurityContext{
						Capabilities: &v1.Capabilities{
							Drop: []v1.Capability{"ALL"},
						},
						AllowPrivilegeEscalation: &falseValue,
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "test-volume",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
			SecurityContext: &v1.PodSecurityContext{
				RunAsNonRoot: &trueValue,
				SELinuxOptions: &v1.SELinuxOptions{
					Level: selinuxLevel,
				},
				SeccompProfile: &v1.SeccompProfile{
					Type: v1.SeccompProfileTypeRuntimeDefault,
				},
			},
		},
	}
}

func waitForOperatorCondition(ctx context.Context, opClient opclient.Interface, conditionType string, expectedStatus operatorapi.ConditionStatus) error {
	return wait.PollUntilContextCancel(ctx, waitPollInterval, true, func(ctx context.Context) (bool, error) {
		storage, err := opClient.OperatorV1().Storages().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Info("Failed to get Storage CR, retrying", "error", err)
			return false, nil
		}
		return v1helpers.IsOperatorConditionPresentAndEqual(storage.Status.Conditions, conditionType, expectedStatus), nil
	})
}

func getThanosQuerierURL(ctx context.Context, dynClient dynamic.Interface) (string, error) {
	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}
	route, err := dynClient.Resource(routeGVR).Namespace("openshift-monitoring").Get(ctx, "thanos-querier", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get thanos-querier route: %w", err)
	}
	spec, ok := route.Object["spec"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("thanos-querier route has no spec")
	}
	host, ok := spec["host"].(string)
	if !ok || host == "" {
		return "", fmt.Errorf("thanos-querier route has no host")
	}
	return fmt.Sprintf("https://%s", host), nil
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]interface{}    `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func queryPrometheusMetric(ctx context.Context, httpClient *http.Client, thanosURL, query string) (float64, error) {
	reqURL := fmt.Sprintf("%s/api/v1/query?query=%s", thanosURL, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var queryResp prometheusQueryResponse
	if err := json.Unmarshal(body, &queryResp); err != nil {
		return 0, fmt.Errorf("failed to parse prometheus response: %w", err)
	}
	if queryResp.Status != "success" {
		return 0, fmt.Errorf("prometheus query returned status: %s", queryResp.Status)
	}

	var total float64
	for _, result := range queryResp.Data.Result {
		if len(result.Value) >= 2 {
			if valStr, ok := result.Value[1].(string); ok {
				var val float64
				fmt.Sscanf(valStr, "%f", &val)
				total += val
			}
		}
	}
	return total, nil
}

type prometheusAlertsResponse struct {
	Status string `json:"status"`
	Data   struct {
		Alerts []struct {
			Labels map[string]string `json:"labels"`
			State  string            `json:"state"`
		} `json:"alerts"`
	} `json:"data"`
}

func getAlertState(ctx context.Context, httpClient *http.Client, thanosURL, alertName string) (string, error) {
	reqURL := fmt.Sprintf("%s/api/v1/alerts", thanosURL)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("alerts query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var alertsResp prometheusAlertsResponse
	if err := json.Unmarshal(body, &alertsResp); err != nil {
		return "", fmt.Errorf("failed to parse alerts response: %w", err)
	}

	for _, alert := range alertsResp.Data.Alerts {
		if alert.Labels["alertname"] == alertName {
			return alert.State, nil
		}
	}
	return "", nil
}
