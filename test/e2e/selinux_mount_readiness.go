package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	g "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	cfgclientset "github.com/openshift/client-go/config/clientset/versioned"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	v1 "k8s.io/api/core/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

const (
	selinuxReadinessConditionType = "SELinuxMountGAReadinessControllerUpgradeable"
	selinuxConflictMetric         = "selinux_warning_controller_selinux_volume_conflict"
	selinuxReadinessAlertName     = "SELinuxMountGAReadinessWorkloadsDetected"

	selinuxTestLevelCompatible  = "s0:c0,c0"
	selinuxTestLevelConflicting = "s0:c1,c1"
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

			testContext context.Context
			cancel      context.CancelFunc
			ns          *v1.Namespace
			pvc         *v1.PersistentVolumeClaim
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

			// Use a standalone context: Ginkgo cancels the context passed to
			// BeforeEach when the hook returns, before the It block runs.
			testContext, cancel = context.WithTimeout(context.Background(), testTimeout)

			skipUnlessCSIPlatform(testContext, configClient)

			ns = createSelinuxReadinessNamespace(testContext, kubeClient)
			pvc = createSelinuxReadinessPVC(testContext, kubeClient, ns.Name)
		})

		g.AfterEach(func() {
			if ns != nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), testTimeout)
				cleanupSelinuxReadinessNamespace(cleanupCtx, kubeClient, ns.Name)
				cleanupCancel()
				ns = nil
				pvc = nil
			}
			if cancel != nil {
				cancel()
				cancel = nil
			}
		})

		g.It("should block upgrades when pods with conflicting SELinux labels share a volume", func() {
			g.By(fmt.Sprintf("Creating pod with SELinux level %s", selinuxTestLevelCompatible))
			pod1 := newSelinuxTestPod(ns.Name, "selinux-test-pod-1", pvc.Name, selinuxTestLevelCompatible)
			pod1, err := kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod1, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod1: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod1.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod1")
				}
			}()
			g.GinkgoLogr.Info("Created pod", "pod", pod1.Name)

			g.By(fmt.Sprintf("Creating pod with SELinux level %s", selinuxTestLevelConflicting))
			pod2 := newSelinuxTestPod(ns.Name, "selinux-test-pod-2", pvc.Name, selinuxTestLevelConflicting)
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
			httpClient, thanosURL, err := newThanosQuerierHTTPClient(testContext, kubeConfig, kubeClient, dynClient)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to set up Thanos Querier client: %v", err))
			}

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

		g.It("should remain upgradeable when pods with compatible SELinux labels share a volume", func() {
			g.By(fmt.Sprintf("Creating first pod with SELinux level %s", selinuxTestLevelCompatible))
			pod1 := newSelinuxTestPod(ns.Name, "selinux-test-pod-1", pvc.Name, selinuxTestLevelCompatible)
			pod1, err := kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod1, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod1: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod1.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod1")
				}
			}()

			g.By("Waiting for first pod to reach Running")
			if err := waitForPodRunning(testContext, kubeClient, ns.Name, pod1.Name); err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for pod1 to run: %v", err))
			}

			g.By(fmt.Sprintf("Creating second pod with the same SELinux level %s", selinuxTestLevelCompatible))
			pod2 := newSelinuxTestPod(ns.Name, "selinux-test-pod-2", pvc.Name, selinuxTestLevelCompatible)
			pod2, err = kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod2, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod2: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod2.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod2")
				}
			}()

			g.By("Waiting for second pod to reach Running")
			if err := waitForPodRunning(testContext, kubeClient, ns.Name, pod2.Name); err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for pod2 to run: %v", err))
			}

			g.By("Verifying SELinuxMountGAReadinessControllerUpgradeable stays True")
			err = waitForOperatorCondition(testContext, opClient, selinuxReadinessConditionType, operatorapi.ConditionTrue)
			if err != nil {
				g.Fail(fmt.Sprintf("Cluster became un-upgradeable with compatible SELinux labels: %v", err))
			}
		})

		g.It("should become upgradeable again when a conflicting pod is recreated with a compatible SELinux label", func() {
			g.By(fmt.Sprintf("Creating pod with SELinux level %s", selinuxTestLevelCompatible))
			pod1 := newSelinuxTestPod(ns.Name, "selinux-test-pod-1", pvc.Name, selinuxTestLevelCompatible)
			pod1, err := kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod1, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod1: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod1.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod1")
				}
			}()

			g.By("Waiting for first pod to reach Running")
			if err := waitForPodRunning(testContext, kubeClient, ns.Name, pod1.Name); err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for pod1 to run: %v", err))
			}

			g.By(fmt.Sprintf("Creating pod with conflicting SELinux level %s", selinuxTestLevelConflicting))
			pod2 := newSelinuxTestPod(ns.Name, "selinux-test-pod-2", pvc.Name, selinuxTestLevelConflicting)
			pod2, err = kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod2, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create pod2: %v", err))
			}

			g.By("Waiting for SELinuxMountGAReadinessControllerUpgradeable=False")
			err = waitForOperatorCondition(testContext, opClient, selinuxReadinessConditionType, operatorapi.ConditionFalse)
			if err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for Upgradeable=False: %v", err))
			}

			g.By("Deleting conflicting pod to allow recreation with a compatible SELinux label")
			if err := deletePodWithWait(testContext, kubeClient, pod2.Name, ns.Name); err != nil {
				g.Fail(fmt.Sprintf("Failed to delete pod2: %v", err))
			}

			g.By(fmt.Sprintf("Recreating pod with compatible SELinux level %s", selinuxTestLevelCompatible))
			pod2 = newSelinuxTestPod(ns.Name, "selinux-test-pod-2", pvc.Name, selinuxTestLevelCompatible)
			pod2, err = kubeClient.CoreV1().Pods(ns.Name).Create(testContext, pod2, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to recreate pod2: %v", err))
			}
			defer func() {
				if err := deletePodWithWait(testContext, kubeClient, pod2.Name, ns.Name); err != nil {
					g.GinkgoLogr.Error(err, "Failed to delete pod2")
				}
			}()

			g.By("Waiting for recreated pod to reach Running")
			if err := waitForPodRunning(testContext, kubeClient, ns.Name, pod2.Name); err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for recreated pod2 to run: %v", err))
			}

			g.By("Waiting for SELinuxMountGAReadinessControllerUpgradeable=True")
			err = waitForOperatorCondition(testContext, opClient, selinuxReadinessConditionType, operatorapi.ConditionTrue)
			if err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for Upgradeable=True after fixing SELinux label: %v", err))
			}
		})
	})

func skipUnlessCSIPlatform(ctx context.Context, configClient cfgclientset.Interface) {
	g.By("Checking platform type")
	infra, err := configClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("Failed to get infrastructure: %v", err))
	}
	switch infra.Status.PlatformStatus.Type {
	case configv1.BareMetalPlatformType, configv1.NonePlatformType:
		g.Skip(fmt.Sprintf("Test not applicable for platform %s (no default CSI storage)", infra.Status.PlatformStatus.Type))
	}
}

func createSelinuxReadinessNamespace(ctx context.Context, kubeClient kubernetes.Interface) *v1.Namespace {
	g.By("Creating test namespace")
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "selinux-readiness-test-",
		},
	}
	ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("Failed to create namespace: %v", err))
	}
	g.GinkgoLogr.Info("Created namespace", "namespace", ns.Name)
	return ns
}

func cleanupSelinuxReadinessNamespace(ctx context.Context, kubeClient kubernetes.Interface, namespace string) {
	g.By(fmt.Sprintf("Cleaning up namespace %s", namespace))
	if err := cleanupNamespace(ctx, kubeClient, namespace); err != nil {
		g.GinkgoLogr.Error(err, "Failed to cleanup namespace")
	}
}

func createSelinuxReadinessPVC(ctx context.Context, kubeClient kubernetes.Interface, namespace string) *v1.PersistentVolumeClaim {
	g.By("Creating RWO PVC with default StorageClass")
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "selinux-readiness-test-",
			Namespace:    namespace,
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
	pvc, err := kubeClient.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("Failed to create PVC: %v", err))
	}
	g.GinkgoLogr.Info("Created PVC", "pvc", pvc.Name)
	return pvc
}

func waitForPodRunning(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string) error {
	return wait.PollUntilContextCancel(ctx, waitPollInterval, true, func(ctx context.Context) (bool, error) {
		pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Info("Failed to get pod, retrying", "pod", name, "error", err)
			return false, nil
		}
		switch pod.Status.Phase {
		case v1.PodRunning:
			return true, nil
		case v1.PodFailed:
			return false, fmt.Errorf("pod %s/%s failed", namespace, name)
		default:
			g.GinkgoLogr.Info("Waiting for pod to run", "pod", name, "phase", pod.Status.Phase)
			return false, nil
		}
	})
}

func newSelinuxTestPod(ns, name, pvcName, selinuxLevel string) *v1.Pod {
	falseValue := false
	trueValue := true
	var nonRootUser int64 = 1000

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
				RunAsUser:    &nonRootUser,
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

func newThanosQuerierHTTPClient(ctx context.Context, kubeConfig *rest.Config, kubeClient kubernetes.Interface, dynClient dynamic.Interface) (*http.Client, string, error) {
	thanosURL, err := getThanosQuerierURL(ctx, dynClient)
	if err != nil {
		return nil, "", err
	}

	token, err := getMonitoringBearerToken(ctx, kubeConfig, kubeClient)
	if err != nil {
		return nil, "", err
	}

	baseTransport, err := rest.TransportFor(kubeConfig)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create HTTP transport: %w", err)
	}

	return &http.Client{
		Transport: transport.NewBearerAuthRoundTripper(token, baseTransport),
	}, thanosURL, nil
}

func getMonitoringBearerToken(ctx context.Context, kubeConfig *rest.Config, kubeClient kubernetes.Interface) (string, error) {
	if kubeConfig.BearerToken != "" {
		return kubeConfig.BearerToken, nil
	}
	if kubeConfig.BearerTokenFile != "" {
		content, err := os.ReadFile(kubeConfig.BearerTokenFile)
		if err != nil {
			return "", fmt.Errorf("failed to read bearer token file: %w", err)
		}
		if token := strings.TrimSpace(string(content)); token != "" {
			return token, nil
		}
	}

	secrets, err := kubeClient.CoreV1().Secrets("openshift-monitoring").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list secrets in openshift-monitoring: %w", err)
	}
	for _, secret := range secrets.Items {
		if secret.Type != v1.SecretTypeServiceAccountToken || !strings.HasPrefix(secret.Name, "prometheus-k8s") {
			continue
		}
		if token := string(secret.Data[v1.ServiceAccountTokenKey]); token != "" {
			return token, nil
		}
	}

	tokenReq, err := kubeClient.CoreV1().ServiceAccounts("openshift-monitoring").CreateToken(
		ctx,
		"prometheus-k8s",
		&authenticationv1.TokenRequest{},
		metav1.CreateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("no bearer token found in kubeconfig or prometheus-k8s service account: %w", err)
	}
	if tokenReq.Status.Token == "" {
		return "", fmt.Errorf("prometheus-k8s service account token request returned an empty token")
	}
	return tokenReq.Status.Token, nil
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
		if status, ok := route.Object["status"].(map[string]interface{}); ok {
			if ingress, ok := status["ingress"].([]interface{}); ok && len(ingress) > 0 {
				if entry, ok := ingress[0].(map[string]interface{}); ok {
					host, _ = entry["host"].(string)
				}
			}
		}
	}
	if host == "" {
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
