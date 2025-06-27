package e2e

import (
	"context"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

const (
	clientName         = "cluster-storage-operator-e2e"
	storageCOName      = "storage"
	fsGroupPolicyLabel = "storage.openshift.io/fsgroup-change-policy"
)

var (
	waitPollInterval = time.Second
	testTimeout      = 10 * time.Minute
)

// newClientConfigForTest returns a config configured to connect to the api server
func newClientConfigForTest(t *testing.T) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, &clientcmd.ConfigOverrides{ClusterInfo: api.Cluster{InsecureSkipTLSVerify: true}})
	config, err := clientConfig.ClientConfig()
	if err == nil {
		t.Logf("Found configuration for host %v", config.Host)
	}
	return config, err
}

func TestFSgroupChangePolicyDefault(t *testing.T) {
	kubeConfig, err := newClientConfigForTest(t)
	if err != nil {
		t.Fatalf("Failed to get kubeconfig: %v", err)
	}

	kubeClient := kubernetes.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))

	configClient := configclient.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))
	t.Logf("Waiting for storage to be available")
	testContext, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	waitForStorageAvailable(testContext, t, configClient)

	nsObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fsgroup-policy-test-",
			Labels: map[string]string{
				fsGroupPolicyLabel: string(v1.FSGroupChangeOnRootMismatch),
			},
		},
	}
	nsObj, err = kubeClient.CoreV1().Namespaces().Create(testContext, nsObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("Failed to create test namespace: %v", err)
	}
	t.Logf("Created namespace %q with label storage.openshift.io/fsgroup-change-policy=OnRootMismatch", nsObj.Name)
	defer cleanupNamespace(testContext, t, kubeClient, nsObj.Name)
	pod := getPod(nsObj.Name)

	createdPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Create(testContext, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	t.Logf("Created pod %q in namespace %q", createdPod.Name, nsObj.Name)

	// Wait for pod to be scheduled
	err = wait.PollUntilContextCancel(testContext, waitPollInterval, false, func(ctx context.Context) (bool, error) {
		p, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return p.Status.Phase != v1.PodPending, nil
	})
	if err != nil {
		t.Fatalf("Pod did not schedule: %v", err)
	}

	// Get the pod and check FSGroupChangePolicy
	runningPod, err := kubeClient.CoreV1().Pods(nsObj.Name).Get(testContext, createdPod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get pod: %v", err)
	}
	if runningPod.Spec.SecurityContext == nil || runningPod.Spec.SecurityContext.FSGroupChangePolicy == nil {
		t.Fatalf("FSGroupChangePolicy not set on pod")
	}
	if *runningPod.Spec.SecurityContext.FSGroupChangePolicy != v1.FSGroupChangeOnRootMismatch {
		t.Fatalf("FSGroupChangePolicy = %v, want %v", *runningPod.Spec.SecurityContext.FSGroupChangePolicy, v1.FSGroupChangeOnRootMismatch)
	}
	t.Logf("Pod FSGroupChangePolicy correctly set to %v", *runningPod.Spec.SecurityContext.FSGroupChangePolicy)
}

func cleanupNamespace(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, namespace string) {
	t.Logf("Cleaning up namespace %q", namespace)
	err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("Failed to delete test namespace %q: %v", namespace, err)
	}

	// Wait for the namespace to be deleted
	err = wait.PollUntilContextCancel(ctx, waitPollInterval, false, func(ctx context.Context) (bool, error) {
		_, err := kubeClient.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil // Namespace deleted successfully
		}
		if err != nil {
			t.Logf("Error checking namespace %q: %v", namespace, err)
			return false, err // Error occurred, retry
		}
		t.Logf("Namespace %q still exists, waiting for deletion", namespace)
		return false, nil // Namespace still exists, keep waiting
	})

	if err != nil {
		t.Fatalf("Failed to wait for namespace %q deletion: %v", namespace, err)
	}
	// Log successful deletion
	t.Logf("Namespace %q deleted successfully", namespace)
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

func waitForStorageAvailable(ctx context.Context, t *testing.T, client *configclient.Clientset) {
	err := wait.PollUntilContextCancel(ctx, waitPollInterval, false, func(pollContext context.Context) (bool, error) {
		clusterOperator, err := client.ConfigV1().ClusterOperators().Get(pollContext, storageCOName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Log("ClusterOperator/storage does not yet exist.")
			return false, nil
		}
		if err != nil {
			t.Log("Unable to retrieve ClusterOperator/storage:", err)
			return false, err
		}
		conditions := clusterOperator.Status.Conditions
		available := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, configv1.ConditionTrue)
		notProgressing := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, configv1.ConditionFalse)
		notDegraded := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorDegraded, configv1.ConditionFalse)
		done := available && notProgressing && notDegraded
		t.Logf("ClusterOperator/storage: Available: %v  Progressing: %v  Degraded: %v\n", available, !notProgressing, !notDegraded)
		return done, nil
	})

	if err != nil {
		t.Fatal(err)
	}
}
