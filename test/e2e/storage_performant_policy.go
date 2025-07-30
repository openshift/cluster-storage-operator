package e2e

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	g "github.com/onsi/ginkgo/v2"
)

const (
	clientName         = "cluster-storage-operator-e2e"
	storageCOName      = "storage"
	fsGroupPolicyLabel = "storage.openshift.io/fsgroup-change-policy"
	selinuxPolicyLabel = "storage.openshift.io/selinux-change-policy"
)

var (
	waitPollInterval = time.Second
)

// newClientConfigForTest returns a config configured to connect to the api server
func newClientConfigForTest() (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, &clientcmd.ConfigOverrides{ClusterInfo: api.Cluster{InsecureSkipTLSVerify: true}})
	config, err := clientConfig.ClientConfig()
	if err == nil {
		g.GinkgoLogr.Info("Found configuration for", "host", config.Host)
	}
	return config, err
}

var _ = g.Describe("[sig-storage][OCPFeatureGate:StoragePerformantPolicy] Storage Performant Policy", g.Label("Conformance"), g.Label("Parallel"), func() {
	var kubeClient *kubernetes.Clientset

	g.BeforeEach(func() {
		kubeConfig, err := newClientConfigForTest()
		if err != nil {
			g.Fail(fmt.Sprintf("Failed to get kubeconfig: %v", err))
		}
		kubeClient = kubernetes.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))
	})

	g.Context("namespace has fsgroup change policy label", func() {
		var nsObj *v1.Namespace
		g.BeforeEach(func(ctx context.Context) {
			nsObj = &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "fsgroup-policy-test-",
					Labels: map[string]string{
						fsGroupPolicyLabel: string(v1.FSGroupChangeOnRootMismatch),
					},
				},
			}
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			var err error
			nsObj, err = kubeClient.CoreV1().Namespaces().Create(testContext, nsObj, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				g.Fail(fmt.Sprintf("Failed to create test namespace: %v", err))
			}
			g.GinkgoLogr.Info("Created namespace with label storage.openshift.io/fsgroup-change-policy=OnRootMismatch", "namespace", nsObj.Name)
		})

		g.AfterEach(func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			cleanupNamespace(testContext, kubeClient, nsObj.Name)
		})

		g.It("it should set fsgroup change policy to OnRootMismatch if pod has none", func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			pod := getPod(nsObj.Name)

			createdPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Create(testContext, pod, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create test pod: %v", err))
			}
			g.GinkgoLogr.Info("Created pod in namespace", "pod", createdPod.Name, "namespace", nsObj.Name)

			// Wait for pod to be scheduled
			err = wait.PollUntilContextCancel(testContext, waitPollInterval, false, func(ctx context.Context) (bool, error) {
				p, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				return p.Status.Phase != v1.PodPending, nil
			})
			if err != nil {
				g.Fail(fmt.Sprintf("Pod did not schedule: %v", err))
			}

			// Get the pod and check FSGroupChangePolicy
			runningPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(testContext, createdPod.Name, metav1.GetOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get pod: %v", err))
			}
			if runningPod.Spec.SecurityContext == nil || runningPod.Spec.SecurityContext.FSGroupChangePolicy == nil {
				g.Fail("FSGroupChangePolicy not set on pod")
			}
			if *runningPod.Spec.SecurityContext.FSGroupChangePolicy != v1.FSGroupChangeOnRootMismatch {
				g.Fail(fmt.Sprintf("FSGroupChangePolicy = %v, want %v", *runningPod.Spec.SecurityContext.FSGroupChangePolicy, v1.FSGroupChangeOnRootMismatch))
			}
			g.GinkgoLogr.Info("Pod FSGroupChangePolicy correctly set to", "policy", *runningPod.Spec.SecurityContext.FSGroupChangePolicy)
		})

		g.It("it should not override fsgroup change policy if pod already has one", func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			pod := getPod(nsObj.Name)

			alwaysChangePolicy := v1.FSGroupChangeAlways
			pod.Spec.SecurityContext.FSGroupChangePolicy = &alwaysChangePolicy

			createdPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Create(testContext, pod, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create test pod: %v", err))
			}
			g.GinkgoLogr.Info("Created pod in namespace", "pod", createdPod.Name, "namespace", nsObj.Name)
			// check if pod has correct fsgroup change policy
			runningPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(testContext, createdPod.Name, metav1.GetOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get pod: %v", err))
			}
			if runningPod.Spec.SecurityContext == nil || runningPod.Spec.SecurityContext.FSGroupChangePolicy == nil {
				g.Fail("FSGroupChangePolicy not set on pod")
			}
			if *runningPod.Spec.SecurityContext.FSGroupChangePolicy != v1.FSGroupChangeAlways {
				g.Fail(fmt.Sprintf("FSGroupChangePolicy = %v, want %v", *runningPod.Spec.SecurityContext.FSGroupChangePolicy, v1.FSGroupChangeAlways))
			}
		})
	})

	g.Context("namespace has selinux change policy label", func() {
		var nsObj *v1.Namespace
		g.BeforeEach(func(ctx context.Context) {
			nsObj = &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "selinux-policy-test-",
					Labels: map[string]string{
						selinuxPolicyLabel: string(v1.SELinuxChangePolicyRecursive),
					},
				},
			}
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			var err error
			nsObj, err = kubeClient.CoreV1().Namespaces().Create(testContext, nsObj, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				g.Fail(fmt.Sprintf("Failed to create test namespace: %v", err))
			}
			g.GinkgoLogr.Info("Created namespace with label storage.openshift.io/selinux-change-policy=Recursive", "namespace", nsObj.Name)
		})

		g.AfterEach(func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			cleanupNamespace(testContext, kubeClient, nsObj.Name)
		})

		g.It("it should set selinux change policy to Recursive if pod has none", func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			pod := getPod(nsObj.Name)
			createdPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Create(testContext, pod, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create test pod: %v", err))
			}
			g.GinkgoLogr.Info("Created pod in namespace", "pod", createdPod.Name, "namespace", nsObj.Name)

			// check if pod has correct selinux change policy
			runningPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(testContext, createdPod.Name, metav1.GetOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get pod: %v", err))
			}
			if runningPod.Spec.SecurityContext == nil || runningPod.Spec.SecurityContext.SELinuxChangePolicy == nil {
				g.Fail("SELinuxChangePolicy not set on pod")
			}
			if *runningPod.Spec.SecurityContext.SELinuxChangePolicy != v1.SELinuxChangePolicyRecursive {
				g.Fail(fmt.Sprintf("SELinuxChangePolicy = %v, want %v", *runningPod.Spec.SecurityContext.SELinuxChangePolicy, v1.SELinuxChangePolicyRecursive))
			}
		})

		g.It("it should not override selinux change policy if pod already has one", func(ctx context.Context) {
			testContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			pod := getPod(nsObj.Name)
			recursiveChangePolicy := v1.SELinuxChangePolicyRecursive
			pod.Spec.SecurityContext.SELinuxChangePolicy = &recursiveChangePolicy

			createdPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Create(testContext, pod, metav1.CreateOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to create test pod: %v", err))
			}
			g.GinkgoLogr.Info("Created pod in namespace", "pod", createdPod.Name, "namespace", nsObj.Name)

			// check if pod has correct selinux change policy
			runningPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(testContext, createdPod.Name, metav1.GetOptions{})
			if err != nil {
				g.Fail(fmt.Sprintf("Failed to get pod: %v", err))
			}
			if runningPod.Spec.SecurityContext == nil || runningPod.Spec.SecurityContext.SELinuxChangePolicy == nil {
				g.Fail("SELinuxChangePolicy not set on pod")
			}
			if *runningPod.Spec.SecurityContext.SELinuxChangePolicy != v1.SELinuxChangePolicyRecursive {
				g.Fail(fmt.Sprintf("SELinuxChangePolicy = %v, want %v", *runningPod.Spec.SecurityContext.SELinuxChangePolicy, v1.SELinuxChangePolicyRecursive))
			}
		})
	})
})

func cleanupNamespace(ctx context.Context, kubeClient kubernetes.Interface, namespace string) {
	g.GinkgoLogr.Info("Cleaning up namespace", "namespace", namespace)
	err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		g.Fail(fmt.Sprintf("Failed to delete test namespace %q: %v", namespace, err))
	}

	// Wait for the namespace to be deleted
	err = wait.PollUntilContextCancel(ctx, waitPollInterval, false, func(ctx context.Context) (bool, error) {
		_, err := kubeClient.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil // Namespace deleted successfully
		}
		if err != nil {
			g.GinkgoLogr.Error(err, "Error checking namespace", "namespace", namespace)
			return false, err // Error occurred, retry
		}
		g.GinkgoLogr.Info("Namespace still exists, waiting for deletion", "namespace", namespace)
		return false, nil // Namespace still exists, keep waiting
	})

	if err != nil {
		g.Fail(fmt.Sprintf("Failed to wait for namespace %q deletion: %v", namespace, err))
	}
	// Log successful deletion
	g.GinkgoLogr.Info("Namespace deleted successfully", "namespace", namespace)
}

func getPod(ns string) *v1.Pod {
	falseValue := false
	trueValue := true
	uidGIDValue := int64(1000)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fsgroup-policy-test-pod-",
			Namespace:    ns,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    "pause",
					Image:   "k8s.gcr.io/pause:3.2",
					Command: []string{"/pause"},
					SecurityContext: &v1.SecurityContext{
						Capabilities: &v1.Capabilities{
							Drop: []v1.Capability{"ALL"},
						},
						AllowPrivilegeEscalation: &falseValue,
					},
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
			SecurityContext: &v1.PodSecurityContext{
				RunAsNonRoot: &trueValue,
				SeccompProfile: &v1.SeccompProfile{
					Type: v1.SeccompProfileTypeRuntimeDefault,
				},
				RunAsUser:  &uidGIDValue,
				RunAsGroup: &uidGIDValue,
				FSGroup:    &uidGIDValue,
			},
		},
	}
	return pod
}
