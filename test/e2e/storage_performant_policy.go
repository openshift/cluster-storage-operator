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
	fsGroupPolicyLabel = "storage.openshift.io/fsgroup-change-policy"
	selinuxPolicyLabel = "storage.openshift.io/selinux-change-policy"

	PauseImage = "k8s.gcr.io/pause:3.2"
)

var (
	waitPollInterval = 2 * time.Second
	testTimeout      = 10 * time.Minute
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

var _ = g.Describe("[sig-storage] Storage Performant Policy", g.Label("Conformance"), g.Label("Parallel"), func() {
	var kubeClient *kubernetes.Clientset

	g.BeforeEach(func() {
		kubeConfig, err := newClientConfigForTest()
		if err != nil {
			g.Fail(fmt.Sprintf("Failed to get kubeconfig: %v", err))
		}
		kubeClient = kubernetes.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))
	})

	g.Context("with valid namespace labels on", func() {
		tests := []struct {
			name                 string
			whenCondition        string
			applySecurityContext func(pod *v1.Pod) *v1.Pod
			namespaceLabel       string
			namespaceLabelValue  string
			checkSecurityContext func(pod *v1.Pod) bool
		}{
			{
				name:          "should default to OnRootMismatch if pod has none",
				whenCondition: "fsgroup",
				applySecurityContext: func(pod *v1.Pod) *v1.Pod {
					return pod
				},
				namespaceLabel:      fsGroupPolicyLabel,
				namespaceLabelValue: string(v1.FSGroupChangeOnRootMismatch),
				checkSecurityContext: func(pod *v1.Pod) bool {
					if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.FSGroupChangePolicy == nil {
						return false
					}
					return *pod.Spec.SecurityContext.FSGroupChangePolicy == v1.FSGroupChangeOnRootMismatch
				},
			},
			{
				name:          "should not override fsgroup change policy if pod already has one",
				whenCondition: "fsgroup",
				applySecurityContext: func(pod *v1.Pod) *v1.Pod {
					alwaysChangePolicy := v1.FSGroupChangeAlways
					pod.Spec.SecurityContext.FSGroupChangePolicy = &alwaysChangePolicy
					return pod
				},
				namespaceLabel:      fsGroupPolicyLabel,
				namespaceLabelValue: string(v1.FSGroupChangeAlways),
				checkSecurityContext: func(pod *v1.Pod) bool {
					if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.FSGroupChangePolicy == nil {
						return false
					}
					return *pod.Spec.SecurityContext.FSGroupChangePolicy == v1.FSGroupChangeAlways
				},
			},
			{
				name:          "should not override selinux change policy if pod already has one",
				whenCondition: "selinux",
				applySecurityContext: func(pod *v1.Pod) *v1.Pod {
					recursiveChangePolicy := v1.SELinuxChangePolicyRecursive
					pod.Spec.SecurityContext.SELinuxChangePolicy = &recursiveChangePolicy
					return pod
				},
				namespaceLabel:      selinuxPolicyLabel,
				namespaceLabelValue: string(v1.SELinuxChangePolicyRecursive),
				checkSecurityContext: func(pod *v1.Pod) bool {
					if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.SELinuxChangePolicy == nil {
						return false
					}
					return *pod.Spec.SecurityContext.SELinuxChangePolicy == v1.SELinuxChangePolicyRecursive
				},
			},
			{
				name:          "should default to selinux label of namespace if pod has none",
				whenCondition: "selinux",
				applySecurityContext: func(pod *v1.Pod) *v1.Pod {
					return pod
				},
				namespaceLabel:      selinuxPolicyLabel,
				namespaceLabelValue: string(v1.SELinuxChangePolicyRecursive),
				checkSecurityContext: func(pod *v1.Pod) bool {
					if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.SELinuxChangePolicy == nil {
						return false
					}
					return *pod.Spec.SecurityContext.SELinuxChangePolicy == v1.SELinuxChangePolicyRecursive
				},
			},
		}

		for _, test := range tests {
			tc := test
			g.When(tc.whenCondition, func() {
				var nsObj *v1.Namespace
				var pod *v1.Pod

				g.BeforeEach(func(ctx context.Context) {
					nsObj = &v1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "security-policy-test-",
							Labels: map[string]string{
								tc.namespaceLabel: tc.namespaceLabelValue,
							},
						},
					}
					testContext, cancel := context.WithTimeout(ctx, testTimeout)
					defer cancel()
					var err error
					g.By("Creating namespace for testing storage-performant-policy")
					nsObj, err = kubeClient.CoreV1().Namespaces().Create(testContext, nsObj, metav1.CreateOptions{})
					if err != nil && !apierrors.IsAlreadyExists(err) {
						g.Fail(fmt.Sprintf("Failed to create test namespace: %v", err))
					}
					g.GinkgoLogr.Info("Created namespace with label", "namespace", nsObj.Name, "label", tc.namespaceLabel, "value", tc.namespaceLabelValue)
				})
				g.AfterEach(func(ctx context.Context) {
					testContext, cancel := context.WithTimeout(ctx, testTimeout)
					defer cancel()
					g.By(fmt.Sprintf("Deleting namespace %s", nsObj.Name))
					err := cleanupNamespace(testContext, kubeClient, nsObj.Name)
					if err != nil {
						g.Fail(fmt.Sprintf("Failed to delete namespace: %v", err))
					}
				})

				g.It(tc.name, func(ctx context.Context) {
					testContext, cancel := context.WithTimeout(ctx, testTimeout)
					defer cancel()
					pod = getPod(nsObj.Name)
					pod = tc.applySecurityContext(pod)

					var err error

					g.By("Creating pod for storage-performant-policy")
					pod, err = kubeClient.CoreV1().Pods(nsObj.Name).Create(testContext, pod, metav1.CreateOptions{})
					if err != nil {
						g.Fail(fmt.Sprintf("Failed to create test pod: %v", err))
					}
					g.GinkgoLogr.Info("Created pod in namespace", "pod", pod.Name, "namespace", nsObj.Name)
					defer func() {
						g.By(fmt.Sprintf("Deleting pod %s in namespace %s", pod.Name, nsObj.Name))
						err := deletePodWithWait(testContext, kubeClient, pod.Name, nsObj.Name)
						if err != nil {
							g.Fail(fmt.Sprintf("Failed to delete pod: %v", err))
						}
					}()

					g.By("Fetching pod to verify storage security policies")
					// Get the pod and check security context policy
					pod, err = kubeClient.CoreV1().Pods(nsObj.Name).Get(testContext, pod.Name, metav1.GetOptions{})
					if err != nil {
						g.Fail(fmt.Sprintf("Failed to get pod: %v", err))
					}

					g.By("Checking if pod has expected security policy")
					if !tc.checkSecurityContext(pod) {
						g.Fail(fmt.Sprintf("security context policy not set to %v on pod %+s with context: %+v", tc.namespaceLabelValue, pod.Name, pod.Spec.SecurityContext))
					}
				})
			})
		}
	})

	g.Context("with invalid namespace labels on", func() {
		tests := []struct {
			name                string
			namespaceLabel      string
			namespaceLabelValue string
			shouldFail          bool
		}{
			{
				name:                "should fail to create namespace with invalid fsgroup label",
				namespaceLabel:      fsGroupPolicyLabel,
				namespaceLabelValue: "invalid",
				shouldFail:          true,
			},
			{
				name:                "should fail to create namespace with invalid selinux label",
				namespaceLabel:      selinuxPolicyLabel,
				namespaceLabelValue: "invalid",
				shouldFail:          true,
			},
		}

		for _, test := range tests {
			tc := test
			g.It(tc.name, func(ctx context.Context) {
				nsObj := &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "security-policy-test-",
						Labels: map[string]string{
							tc.namespaceLabel: tc.namespaceLabelValue,
						},
					},
				}
				testContext, cancel := context.WithTimeout(ctx, testTimeout)
				defer cancel()
				var err error
				g.By("Creating invalid namespace for storage-performant-policy")
				nsObj, err = kubeClient.CoreV1().Namespaces().Create(testContext, nsObj, metav1.CreateOptions{})
				if err == nil {
					defer func() {
						err = cleanupNamespace(testContext, kubeClient, nsObj.Name)
						if err != nil {
							g.Fail(fmt.Sprintf("Failed to delete namespace: %v", err))
						}
					}()

					if tc.shouldFail {
						g.Fail(fmt.Sprintf("Expected error to be returned when creating namespace with invalid label: %+v", nsObj))
					}
				}
				if err != nil && !tc.shouldFail {
					g.Fail(fmt.Sprintf("Failed to create namespace : %v", err))
				}
			})
		}
	})

})

func cleanupNamespace(ctx context.Context, kubeClient kubernetes.Interface, namespace string) error {
	g.GinkgoLogr.Info("Cleaning up namespace", "namespace", namespace)
	err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete namespace %s: %w", namespace, err)
	}

	// Wait for the namespace to be deleted
	err = wait.PollUntilContextCancel(ctx, waitPollInterval, false, func(ctx context.Context) (bool, error) {
		_, err := kubeClient.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil // Namespace still exists, keep waiting
	})

	if err != nil {
		return fmt.Errorf("failed to wait for namespace %s deletion: %w", namespace, err)
	}
	// Log successful deletion
	g.GinkgoLogr.Info("Namespace deleted successfully", "namespace", namespace)
	return nil
}

func deletePodWithWait(ctx context.Context, kubeClient kubernetes.Interface, podName, podNamespace string) error {
	if podName == "" || podNamespace == "" {
		return nil
	}
	g.GinkgoLogr.Info("Deleting pod", "pod", podName, "namespace", podNamespace)
	err := kubeClient.CoreV1().Pods(podNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("error deleting pod %s/%s: %w", podNamespace, podName, err)
	}
	err = wait.PollUntilContextCancel(ctx, waitPollInterval, false, func(ctx context.Context) (bool, error) {
		_, err := kubeClient.CoreV1().Pods(podNamespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to wait for pod %s/%s deletion: %w", podNamespace, podName, err)
	}
	g.GinkgoLogr.Info("Pod deleted successfully", "pod", podName, "namespace", podNamespace)
	return nil
}

func getPod(ns string) *v1.Pod {
	falseValue := false
	trueValue := true

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fsgroup-policy-test-pod-",
			Namespace:    ns,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    "pause",
					Image:   PauseImage,
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
			},
		},
	}
	return pod
}
