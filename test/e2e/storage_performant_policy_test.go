package e2e

import (
	"context"
	"testing"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/vmware-vsphere-csi-driver-operator/test/library"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	clientName        = "vsphere-operator-e2e"
	storageCOName     = "storage"
	csiDriverName     = "csi.vsphere.vmware.com"
	disabledCondition = "VMwareVSphereControllerDisabled"

	csiDriverNameSpace          = "openshift-cluster-csi-drivers"
	csiControllerDeploymentName = "vmware-vsphere-csi-driver-controller"
	csiNodeDaemonSetName        = "vmware-vsphere-csi-driver-node"
)

func TestFSgroupChangePolicyDefault(t *testing.T) {
	kubeConfig, err := library.NewClientConfigForTest(t)
	if err != nil {
		t.Fatalf("Failed to get kubeconfig: %v", err)
	}

	kubeClient := kubernetes.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))
	ocpOperatorClient := operatorclient.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))

	configClient := configclient.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))
	t.Logf("Waiting for storage to be available")
	waitForStorageAvailable(t, configClient, ocpOperatorClient, kubeClient)
}

func waitForStorageAvailable(t *testing.T, client *configclient.Clientset, operatorClient *operatorclient.Clientset, kubeClient *kubernetes.Clientset) {
	err := wait.PollUntilContextTimeout(context.TODO(), WaitPollInterval, WaitPollTimeout, false, func(pollContext context.Context) (bool, error) {
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

		if done {
			disableConditionStatusVar, err := checkDisabledCondition(t, pollContext, operatorClient)
			if err != nil {
				return false, err
			}
			done = disableConditionStatusVar == operatorEnabled
		}

		if done {
			deploymentCreated, err := checkForDeploymentCreation(t, pollContext, kubeClient)
			if err != nil {
				return false, err
			}
			done = deploymentCreated
		}

		if done {
			daemonsetCreated, err := checkForDaemonset(t, pollContext, kubeClient)
			if err != nil {
				return false, err
			}
			done = daemonsetCreated
		}

		t.Logf("ClusterOperator/storage: Available: %v  Progressing: %v  Degraded: %v\n", available, !notProgressing, !notDegraded)
		return done, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
