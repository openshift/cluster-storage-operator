package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	cfgclientset "github.com/openshift/client-go/config/clientset/versioned"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	defaultStorageClassAnnotation = "storageclass.kubernetes.io/is-default-class"

	upgradeableConditionTimeout = 5 * time.Minute
	prometheusQueryTimeout      = 2 * time.Minute
	// The PrometheusRule sets "for: 10m" before the alert transitions to Firing.
	// We only assert the pending state, which should appear shortly after CSO marks
	// the cluster un-upgradeable — well within this timeout.
	alertPendingTimeout = 2 * time.Minute
	podRunningTimeout   = 5 * time.Minute
	setupTimeout        = 5 * time.Minute
	podCleanupTimeout   = 2 * time.Minute
)

// SELinux mount upgrade-readiness end-to-end tests.
//
// # Purpose
//
// Validate that OpenShift blocks cluster upgrades when workloads are incompatible with
// SELinuxMount GA behavior, and that the block clears once conflicts are resolved.
// CSO's SELinuxMountGAReadiness controller reads openshift-config/selinux-conflicts
// (written by the kube-controller-manager SELinux warning controller) and surfaces
// cluster upgrade readiness via:
//   - ClusterOperator/storage condition Upgradeable
//   - Storage operator CR condition SELinuxMountGAReadinessControllerUpgradeable
//   - Prometheus metric selinux_warning_controller_selinux_volume_conflict
//   - Alert SELinuxMountGAReadinessWorkloadsDetected (pending before Firing)
//
// # Preconditions (enforced in BeforeEach / skip helpers)
//
//   - OCP feature gate SELinuxMountGAReadiness enabled ([OCPFeatureGate:...] tag).
//   - At least one default StorageClass whose provisioner CSIDriver has spec.seLinuxMount=true.
//   - openshift-monitoring/thanos-querier reachable (tests query metrics and alerts).
//   - Not MicroShift or disconnected (explicit skip tags; PauseImage and monitoring deps).
//
// # Workload model
//
// Each spec creates an isolated namespace with an RWO PVC (default StorageClass).
// Two pause pods share the PVC on the same node (required for RWO multi-pod scenarios).
// Custom MCS levels (s0:c0,c0 vs s0:c1,c1) exercise label conflicts under MountOption.
// Whether a conflicting second pod reaches Running depends on the release: with SELinuxMount
// still disabled by default (e.g. 5.0 GA readiness gate only), both pods may run; once
// SELinuxMount is GA (e.g. 5.1), pod2 may stay Pending or ContainerCreating. Conflict
// detection is spec-based and does not depend on pod2 phase.
//
// # Execution
//
// Specs run serially (g.Serial) because they mutate cluster-wide Upgradeable state.
// AfterEach deletes the namespace and waits for Upgradeable=True so specs do not leak state.
//
// # Scenarios (four specs)
//
//  1. Conflicting labels → block upgrade, verify metric + pending alert, delete conflict → recover.
//  2. Compatible labels on shared volume → cluster stays upgradeable.
//  3. Conflicting labels → block → delete/recreate pod with compatible label → recover.
//  4. Conflicting labels → block → recreate both pods with SELinuxChangePolicy Recursive → recover.

type selinuxWorkloadOpts struct {
	name         string
	selinuxLevel string
	changePolicy *v1.PodSELinuxChangePolicy
}

type selinuxReadinessEnv struct {
	kubeClient   kubernetes.Interface
	configClient cfgclientset.Interface
	opClient     opclient.Interface
	dynClient    dynamic.Interface
	kubeConfig   *rest.Config
	namespace    string
	pvcName      string
}

var _ = g.Describe("[sig-storage][OCPFeatureGate:SELinuxMountGAReadiness][Skipped:MicroShift][Skipped:Disconnected] SELinux Mount Upgrade Readiness",
	g.Serial,
	g.Label("Conformance"),
	g.Label("Serial"),
	func() {
		var (
			env         selinuxReadinessEnv
			setupCancel context.CancelFunc
			ns          *v1.Namespace
			pvc         *v1.PersistentVolumeClaim
		)

		g.BeforeEach(func() {
			// Per-spec setup: clients, platform capability check, isolated namespace + RWO PVC.
			var err error
			env.kubeConfig, err = newClientConfigForTest()
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get kubeconfig: %v", err))
			}
			agentConfig := rest.AddUserAgent(env.kubeConfig, clientName)
			env.kubeClient = kubernetes.NewForConfigOrDie(agentConfig)
			env.configClient = cfgclientset.NewForConfigOrDie(agentConfig)
			env.opClient = opclient.NewForConfigOrDie(agentConfig)
			env.dynClient = dynamic.NewForConfigOrDie(agentConfig)

			setupCtx, cancel := selinuxReadinessSetupContext()
			setupCancel = cancel

			skipUnlessSELinuxMountDefaultStorage(setupCtx, env.kubeClient)

			ns = createSELinuxReadinessNamespace(setupCtx, env.kubeClient)
			pvc = createSELinuxReadinessPVC(setupCtx, env.kubeClient, ns.Name)
			env.namespace = ns.Name
			env.pvcName = pvc.Name
		})

		g.AfterEach(func() {
			if ns != nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), setupTimeout)
				cleanupSELinuxReadinessNamespace(cleanupCtx, env.kubeClient, ns.Name)
				cleanupCancel()
				ns = nil
				pvc = nil
				env.namespace = ""
				env.pvcName = ""
			}
			if setupCancel != nil {
				setupCancel()
				setupCancel = nil
			}
			// Best-effort recovery so back-to-back specs do not inherit Upgradeable=False.
			if env.configClient != nil && env.opClient != nil {
				recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), upgradeableConditionTimeout)
				if err := waitForClusterUpgradeable(recoveryCtx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
					g.GinkgoLogr.Error(err, "Cluster did not return to Upgradeable=True after test cleanup")
				}
				recoveryCancel()
			}
		})

		// Scenario 1 — detect conflict, observe monitoring signals, recover by removing the offender.
		//
		// Setup: pod1 mounts the PVC with s0:c0,c0 and reaches Running; pod2 is scheduled on
		// the same node with s0:c1,c1 (conflicting MCS level). Whether pod2 reaches Running
		// depends on the release (SELinuxMount disabled vs GA); the test does not assert pod2
		// phase — only that the cluster becomes not upgradeable while the conflict exists.
		// Expected: Upgradeable=False on ClusterOperator/storage and Storage CR; conflict
		// metric > 0; alert in pending state (not yet Firing — rule has for: 10m).
		// Recovery: delete pod2 → Upgradeable=True.
		g.It("should block upgrades when pods with conflicting SELinux labels share a volume", func() {
			ctx := context.Background()

			pod1, pod2, err := env.createSharedPVCPods(ctx,
				selinuxWorkloadOpts{name: "selinux-test-pod-1", selinuxLevel: selinuxTestLevelCompatible},
				selinuxWorkloadOpts{name: "selinux-test-pod-2", selinuxLevel: selinuxTestLevelConflicting},
				false,
			)
			if err != nil {
				g.Fail(err.Error())
			}
			defer deleteSELinuxTestPod(env.kubeClient, pod1.Name, env.namespace)
			pod2Deleted := false
			defer func() {
				if !pod2Deleted {
					deleteSELinuxTestPod(env.kubeClient, pod2.Name, env.namespace)
				}
			}()

			g.By("Waiting for cluster Upgradeable=False")
			if err := waitForClusterNotUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(err.Error())
			}
			if err := env.verifyConflictMonitoring(ctx); err != nil {
				g.Fail(err.Error())
			}

			g.By(fmt.Sprintf("Deleting conflicting pod %s", pod2.Name))
			if err := deletePodWithWait(ctx, env.kubeClient, pod2.Name, env.namespace); err != nil {
				g.Fail(fmt.Sprintf("Failed to delete pod2: %v", err))
			}
			pod2Deleted = true

			g.By("Waiting for cluster Upgradeable=True")
			if err := waitForClusterUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(err.Error())
			}
		})

		// Scenario 2 — negative control: compatible workloads must not block upgrades.
		//
		// Setup: two pods on the same node share the PVC with identical SELinux level s0:c0,c0;
		// both must reach Running (MountOption allows co-mount with matching labels).
		// Expected: cluster remains Upgradeable=True throughout.
		g.It("should remain upgradeable when pods with compatible SELinux labels share a volume", func() {
			ctx := context.Background()

			pod1, pod2, err := env.createSharedPVCPods(ctx,
				selinuxWorkloadOpts{name: "selinux-test-pod-1", selinuxLevel: selinuxTestLevelCompatible},
				selinuxWorkloadOpts{name: "selinux-test-pod-2", selinuxLevel: selinuxTestLevelCompatible},
				true,
			)
			if err != nil {
				g.Fail(err.Error())
			}
			defer deleteSELinuxTestPod(env.kubeClient, pod1.Name, env.namespace)
			defer deleteSELinuxTestPod(env.kubeClient, pod2.Name, env.namespace)

			g.By("Waiting for cluster Upgradeable=True")
			if err := waitForClusterUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(fmt.Sprintf("Cluster became un-upgradeable with compatible SELinux labels: %v", err))
			}
		})

		// Scenario 3 — operator remediation path: align SELinux labels (pod recreate).
		//
		// Pod SELinuxOptions are immutable; fixing a conflict requires delete + recreate.
		// Setup: induce conflict (as in scenario 1), wait for Upgradeable=False.
		// Remediation: delete pod2, recreate it with the same label as pod1.
		// Expected: both pods Running, Upgradeable=True.
		g.It("should become upgradeable again when a conflicting pod is recreated with a compatible SELinux label", func() {
			ctx := context.Background()

			pod1, pod2, err := env.createSharedPVCPods(ctx,
				selinuxWorkloadOpts{name: "selinux-test-pod-1", selinuxLevel: selinuxTestLevelCompatible},
				selinuxWorkloadOpts{name: "selinux-test-pod-2", selinuxLevel: selinuxTestLevelConflicting},
				false,
			)
			if err != nil {
				g.Fail(err.Error())
			}
			defer deleteSELinuxTestPod(env.kubeClient, pod1.Name, env.namespace)

			g.By("Waiting for cluster Upgradeable=False")
			if err := waitForClusterNotUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(err.Error())
			}

			g.By("Deleting conflicting pod to allow recreation with a compatible SELinux label")
			if err := deletePodWithWait(ctx, env.kubeClient, pod2.Name, env.namespace); err != nil {
				g.Fail(fmt.Sprintf("Failed to delete pod2: %v", err))
			}

			g.By(fmt.Sprintf("Recreating pod with compatible SELinux level %s on node %s", selinuxTestLevelCompatible, pod1.Spec.NodeName))
			pod2, err = createSELinuxTestPod(ctx, env.kubeClient, env.namespace, "selinux-test-pod-2", env.pvcName, selinuxWorkloadOpts{
				name:         "selinux-test-pod-2",
				selinuxLevel: selinuxTestLevelCompatible,
			}, pod1.Spec.NodeName, true)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to recreate pod2: %v", err))
			}
			defer deleteSELinuxTestPod(env.kubeClient, pod2.Name, env.namespace)

			g.By("Waiting for cluster Upgradeable=True")
			if err := waitForClusterUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for Upgradeable=True after fixing SELinux label: %v", err))
			}
		})

		// Scenario 4 — alternative remediation: SELinuxChangePolicy Recursive on both pods.
		//
		// Recursive relabeling allows distinct MCS levels on a shared volume without conflict.
		// Setup: induce conflict with default MountOption behavior, wait for Upgradeable=False.
		// Remediation: delete both pods, recreate with distinct labels but Recursive on each.
		// Expected: both pods Running, Upgradeable=True.
		g.It("should become upgradeable again when conflicting pods are recreated with SELinuxChangePolicy Recursive", func() {
			ctx := context.Background()
			recursive := v1.SELinuxChangePolicyRecursive

			pod1, pod2, err := env.createSharedPVCPods(ctx,
				selinuxWorkloadOpts{name: "selinux-test-pod-1", selinuxLevel: selinuxTestLevelCompatible},
				selinuxWorkloadOpts{name: "selinux-test-pod-2", selinuxLevel: selinuxTestLevelConflicting},
				false,
			)
			if err != nil {
				g.Fail(err.Error())
			}
			nodeName := pod1.Spec.NodeName

			g.By("Waiting for cluster Upgradeable=False")
			if err := waitForClusterNotUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(err.Error())
			}

			g.By("Deleting conflicting pods to allow recreation with SELinuxChangePolicy Recursive")
			if err := deletePodWithWait(ctx, env.kubeClient, pod2.Name, env.namespace); err != nil {
				g.Fail(fmt.Sprintf("Failed to delete pod2: %v", err))
			}
			if err := deletePodWithWait(ctx, env.kubeClient, pod1.Name, env.namespace); err != nil {
				g.Fail(fmt.Sprintf("Failed to delete pod1: %v", err))
			}

			g.By("Recreating pods with SELinuxChangePolicy Recursive while keeping distinct SELinux labels")
			pod1, err = createSELinuxTestPod(ctx, env.kubeClient, env.namespace, "selinux-test-pod-1", env.pvcName, selinuxWorkloadOpts{
				name:         "selinux-test-pod-1",
				selinuxLevel: selinuxTestLevelCompatible,
				changePolicy: &recursive,
			}, nodeName, true)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to recreate pod1: %v", err))
			}
			defer deleteSELinuxTestPod(env.kubeClient, pod1.Name, env.namespace)

			pod2, err = createSELinuxTestPod(ctx, env.kubeClient, env.namespace, "selinux-test-pod-2", env.pvcName, selinuxWorkloadOpts{
				name:         "selinux-test-pod-2",
				selinuxLevel: selinuxTestLevelConflicting,
				changePolicy: &recursive,
			}, nodeName, true)
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to recreate pod2: %v", err))
			}
			defer deleteSELinuxTestPod(env.kubeClient, pod2.Name, env.namespace)

			g.By("Waiting for cluster Upgradeable=True")
			if err := waitForClusterUpgradeable(ctx, env.configClient, env.opClient, upgradeableConditionTimeout); err != nil {
				g.Fail(fmt.Sprintf("Timed out waiting for Upgradeable=True after setting Recursive SELinuxChangePolicy: %v", err))
			}
		})
	})

func selinuxReadinessSetupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), setupTimeout)
}

func (env *selinuxReadinessEnv) createSharedPVCPods(ctx context.Context, pod1Opts, pod2Opts selinuxWorkloadOpts, waitForSecondRunning bool) (*v1.Pod, *v1.Pod, error) {
	// pod1 must be Running so we know which node to pin pod2 on for the shared RWO PVC.
	// waitForSecondRunning=false for conflict cases: do not wait on pod2 phase; upgrade
	// blocking is driven by the conflicting workload spec, not whether pod2 mounts.
	g.By(fmt.Sprintf("Creating pod with SELinux level %s", pod1Opts.selinuxLevel))
	pod1, err := createSELinuxTestPod(ctx, env.kubeClient, env.namespace, pod1Opts.name, env.pvcName, pod1Opts, "", true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create pod1: %w", err)
	}

	g.By(fmt.Sprintf("Creating pod with SELinux level %s on the same node as pod1", pod2Opts.selinuxLevel))
	if !waitForSecondRunning {
		g.By("Not waiting for the second pod to reach Running; conflict detection does not depend on pod2 phase")
	}
	pod2, err := createSELinuxTestPod(ctx, env.kubeClient, env.namespace, pod2Opts.name, env.pvcName, pod2Opts, pod1.Spec.NodeName, waitForSecondRunning)
	if err != nil {
		deleteSELinuxTestPod(env.kubeClient, pod1.Name, env.namespace)
		return nil, nil, fmt.Errorf("failed to create pod2: %w", err)
	}
	return pod1, pod2, nil
}

func (env *selinuxReadinessEnv) verifyConflictMonitoring(ctx context.Context) error {
	// Scenario 1 only: confirm observability beyond the ClusterOperator condition.
	// Alert assertion is pending-only; the PrometheusRule uses for: 10m before Firing.
	g.By("Setting up Prometheus client")
	httpClient, thanosURL, err := newThanosQuerierHTTPClient(ctx, env.kubeConfig, env.kubeClient, env.dynClient)
	if err != nil {
		return fmt.Errorf("failed to set up Thanos Querier client: %w", err)
	}

	g.By("Verifying selinux_warning_controller_selinux_volume_conflict metric > 0")
	err = wait.PollUntilContextTimeout(ctx, waitPollInterval, prometheusQueryTimeout, true, func(ctx context.Context) (bool, error) {
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
		return fmt.Errorf("timed out waiting for metric %s > 0: %w", selinuxConflictMetric, err)
	}

	g.By("Verifying SELinuxMountGAReadinessWorkloadsDetected alert is pending (not yet Firing)")
	err = wait.PollUntilContextTimeout(ctx, waitPollInterval, alertPendingTimeout, true, func(ctx context.Context) (bool, error) {
		state, qErr := getAlertState(ctx, httpClient, thanosURL, selinuxReadinessAlertName)
		if qErr != nil {
			g.GinkgoLogr.Info("Alert query failed, retrying", "error", qErr)
			return false, nil
		}
		if state == "pending" {
			g.GinkgoLogr.Info("Alert is pending", "alertname", selinuxReadinessAlertName)
			return true, nil
		}
		g.GinkgoLogr.Info("Waiting for alert to become pending", "alertname", selinuxReadinessAlertName, "state", state)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for alert %s to become pending: %w", selinuxReadinessAlertName, err)
	}
	return nil
}

func createSELinuxTestPod(ctx context.Context, kubeClient kubernetes.Interface, namespace, name, pvcName string, opts selinuxWorkloadOpts, nodeName string, waitRunning bool) (*v1.Pod, error) {
	pod := newSELinuxPod(namespace, name, pvcName, opts, nodeName)
	created, err := kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	g.GinkgoLogr.Info("Created pod", "pod", created.Name, "node", nodeName, "waitRunning", waitRunning)

	if !waitRunning {
		return created, nil
	}

	if err := waitForPodRunning(ctx, kubeClient, namespace, created.Name); err != nil {
		logPodStatus(ctx, kubeClient, namespace, created.Name)
		deleteSELinuxTestPod(kubeClient, created.Name, namespace)
		return nil, err
	}

	return kubeClient.CoreV1().Pods(namespace).Get(ctx, created.Name, metav1.GetOptions{})
}

func deleteSELinuxTestPod(kubeClient kubernetes.Interface, name, namespace string) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), podCleanupTimeout)
	defer cleanupCancel()
	if err := deletePodWithWait(cleanupCtx, kubeClient, name, namespace); err != nil {
		g.GinkgoLogr.Error(err, "Failed to delete pod", "pod", name)
	}
}

func waitForPodRunning(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, waitPollInterval, podRunningTimeout, true, func(ctx context.Context) (bool, error) {
		pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			g.GinkgoLogr.Info("Failed to get pod, retrying", "pod", name, "error", err)
			return false, nil
		}
		switch pod.Status.Phase {
		case v1.PodRunning:
			return true, nil
		case v1.PodFailed:
			logPodStatus(ctx, kubeClient, namespace, name)
			return false, fmt.Errorf("pod %s/%s failed", namespace, name)
		default:
			g.GinkgoLogr.Info("Waiting for pod to run", "pod", name, "phase", pod.Status.Phase)
			return false, nil
		}
	})
}

func logPodStatus(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string) {
	pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		g.GinkgoLogr.Error(err, "Failed to get pod for diagnostics", "pod", name)
		return
	}
	g.GinkgoLogr.Info("Pod status", "pod", name, "phase", pod.Status.Phase, "node", pod.Spec.NodeName, "message", pod.Status.Message, "reason", pod.Status.Reason)
	for _, cs := range pod.Status.ContainerStatuses {
		g.GinkgoLogr.Info("Container status", "pod", name, "container", cs.Name, "ready", cs.Ready, "state", cs.State)
	}
}

func newSELinuxPod(ns, name, pvcName string, opts selinuxWorkloadOpts, nodeName string) *v1.Pod {
	falseValue := false
	trueValue := true
	var nonRootUser int64 = 1000

	podSecurityContext := &v1.PodSecurityContext{
		RunAsNonRoot: &trueValue,
		RunAsUser:    &nonRootUser,
		SELinuxOptions: &v1.SELinuxOptions{
			Level: opts.selinuxLevel,
		},
		SeccompProfile: &v1.SeccompProfile{
			Type: v1.SeccompProfileTypeRuntimeDefault,
		},
	}
	if opts.changePolicy != nil {
		podSecurityContext.SELinuxChangePolicy = opts.changePolicy
	}

	pod := &v1.Pod{
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
			RestartPolicy:   v1.RestartPolicyNever,
			SecurityContext: podSecurityContext,
		},
	}
	if nodeName != "" {
		pod.Spec.NodeName = nodeName
	}
	return pod
}

func skipUnlessSELinuxMountDefaultStorage(ctx context.Context, kubeClient kubernetes.Interface) {
	// Platform-agnostic gate: the test needs a default CSI driver that advertises
	// spec.seLinuxMount=true. All default StorageClasses must satisfy this; otherwise skip.
	g.By("Checking default StorageClass uses a CSIDriver with SELinuxMount enabled")
	scs, err := kubeClient.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("Failed to list StorageClasses: %v", err))
	}

	var defaultProvisioners []string
	for _, sc := range scs.Items {
		if sc.Annotations[defaultStorageClassAnnotation] == "true" {
			defaultProvisioners = append(defaultProvisioners, sc.Provisioner)
		}
	}
	if len(defaultProvisioners) == 0 {
		g.Skip("No default StorageClass found")
	}

	for _, provisioner := range defaultProvisioners {
		driver, err := kubeClient.StorageV1().CSIDrivers().Get(ctx, provisioner, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				g.Skip(fmt.Sprintf("CSIDriver %q for default StorageClass not found", provisioner))
			}
			g.Fail(fmt.Sprintf("Failed to get CSIDriver %q: %v", provisioner, err))
		}
		if !csiDriverSELinuxMountEnabled(driver) {
			g.Skip(fmt.Sprintf("CSIDriver %q does not have spec.seLinuxMount=true", provisioner))
		}
	}
}

func csiDriverSELinuxMountEnabled(driver *storagev1.CSIDriver) bool {
	return driver.Spec.SELinuxMount != nil && *driver.Spec.SELinuxMount
}

func createSELinuxReadinessNamespace(ctx context.Context, kubeClient kubernetes.Interface) *v1.Namespace {
	g.By("Creating test namespace")
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-selinux-readiness-",
		},
	}
	ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("Failed to create namespace: %v", err))
	}
	g.GinkgoLogr.Info("Created namespace", "namespace", ns.Name)
	return ns
}

func cleanupSELinuxReadinessNamespace(ctx context.Context, kubeClient kubernetes.Interface, namespace string) {
	g.By(fmt.Sprintf("Cleaning up namespace %s", namespace))
	if err := cleanupNamespace(ctx, kubeClient, namespace); err != nil {
		g.GinkgoLogr.Error(err, "Failed to cleanup namespace")
	}
}

func createSELinuxReadinessPVC(ctx context.Context, kubeClient kubernetes.Interface, namespace string) *v1.PersistentVolumeClaim {
	g.By("Creating RWO PVC with default StorageClass")
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-selinux-readiness-",
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

func waitForClusterNotUpgradeable(ctx context.Context, configClient cfgclientset.Interface, opClient opclient.Interface, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, waitPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		if !clusterOperatorUpgradeableIs(ctx, configClient, configv1.ConditionFalse) {
			return false, nil
		}
		return storageOperatorUpgradeableIs(ctx, opClient, operatorapi.ConditionFalse), nil
	})
}

func waitForClusterUpgradeable(ctx context.Context, configClient cfgclientset.Interface, opClient opclient.Interface, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, waitPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		if !clusterOperatorUpgradeableIs(ctx, configClient, configv1.ConditionTrue) {
			return false, nil
		}
		return storageOperatorUpgradeableIs(ctx, opClient, operatorapi.ConditionTrue), nil
	})
}

func clusterOperatorUpgradeableIs(ctx context.Context, configClient cfgclientset.Interface, expected configv1.ConditionStatus) bool {
	co, err := configClient.ConfigV1().ClusterOperators().Get(ctx, "storage", metav1.GetOptions{})
	if err != nil {
		g.GinkgoLogr.Info("Failed to get ClusterOperator storage, retrying", "error", err)
		return false
	}
	for _, cnd := range co.Status.Conditions {
		if cnd.Type == configv1.OperatorUpgradeable {
			if cnd.Status == expected {
				return true
			}
			g.GinkgoLogr.Info("Waiting for ClusterOperator Upgradeable", "expected", expected, "actual", cnd.Status, "reason", cnd.Reason)
			return false
		}
	}
	g.GinkgoLogr.Info("ClusterOperator Upgradeable condition not found, retrying")
	return false
}

func storageOperatorUpgradeableIs(ctx context.Context, opClient opclient.Interface, expected operatorapi.ConditionStatus) bool {
	storage, err := opClient.OperatorV1().Storages().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		g.GinkgoLogr.Info("Failed to get Storage CR, retrying", "error", err)
		return false
	}
	if v1helpers.IsOperatorConditionPresentAndEqual(storage.Status.Conditions, selinuxReadinessConditionType, expected) {
		return true
	}
	g.GinkgoLogr.Info("Waiting for Storage operator condition", "condition", selinuxReadinessConditionType, "expected", expected)
	return false
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

	host, found, err := unstructured.NestedString(route.Object, "spec", "host")
	if err != nil {
		return "", fmt.Errorf("failed to read thanos-querier route spec.host: %w", err)
	}
	if !found || host == "" {
		host, found, err = unstructured.NestedString(route.Object, "status", "ingress", "0", "host")
		if err != nil {
			return "", fmt.Errorf("failed to read thanos-querier route status.ingress host: %w", err)
		}
	}
	if !found || host == "" {
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
				val, err := strconv.ParseFloat(valStr, 64)
				if err != nil {
					return 0, fmt.Errorf("failed to parse metric value %q: %w", valStr, err)
				}
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
