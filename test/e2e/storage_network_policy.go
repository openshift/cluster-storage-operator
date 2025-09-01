package e2e

import (
	"context"
	"fmt"
	"strings"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type DeploymentCheck struct {
	Namespace      string
	Name           string
	Platform       string
	RequiredLabels map[string]string
}

const CSONamespace = "openshift-cluster-storage-operator"
const CSINamespace = "openshift-cluster-csi-drivers"
const ManilaCSINamespace = "openshift-manila-csi-driver"

var (
	CSOOperatorRequiredLabels = map[string]string{
		"openshift.storage.network-policy.dns":              "allow",
		"openshift.storage.network-policy.api-server":       "allow",
		"openshift.storage.network-policy.operator-metrics": "allow",
	}

	CSOControllerRequiredLabels = map[string]string{
		"openshift.storage.network-policy.dns":        "allow",
		"openshift.storage.network-policy.api-server": "allow",
	}

	CSIOperatorRequiredLabels = map[string]string{
		"openshift.storage.network-policy.dns":                    "allow",
		"openshift.storage.network-policy.api-server":             "allow",
		"openshift.storage.network-policy.operator-metrics-range": "allow",
	}

	CSIControllerRequiredLabels = map[string]string{
		"openshift.storage.network-policy.dns":           "allow",
		"openshift.storage.network-policy.api-server":    "allow",
		"openshift.storage.network-policy.metrics-range": "allow",
	}

	NPCSORequiredLabels = CSOOperatorRequiredLabels
	NPCSIRequiredLabels = map[string]string{
		"openshift.storage.network-policy.dns":                    "allow",
		"openshift.storage.network-policy.api-server":             "allow",
		"openshift.storage.network-policy.metrics-range":          "allow",
		"openshift.storage.network-policy.operator-metrics-range": "allow",
	}
)

var _ = g.Describe("[sig-storage][OCPFeatureGate:StorageNetworkPolicy] Storage Network Policy", g.Ordered, g.Label("Conformance"), g.Label("Parallel"), func() {
	var kubeClient *kubernetes.Clientset
	var currentPlatform string

	g.BeforeAll(func() {
		kubeConfig, err := newClientConfigForTest()
		if err != nil {
			g.Fail(fmt.Sprintf("Failed to get kubeconfig: %v", err))
		}
		kubeClient = kubernetes.NewForConfigOrDie(rest.AddUserAgent(kubeConfig, clientName))

		configClient, err := configv1client.NewForConfig(kubeConfig)
		if err != nil {
			g.Fail(fmt.Sprintf("Failed to get config client: %v", err))
		}
		infra, err := configClient.ConfigV1().Infrastructures().Get(context.TODO(), "cluster", metav1.GetOptions{})
		if err != nil {
			g.Fail(fmt.Sprintf("Failed to get infrastructure: %v", err))
		}
		currentPlatform = strings.ToLower(string(infra.Status.PlatformStatus.Type))
	})

	g.It("should verify required labels for CSO related Operators", func() {
		CSODeploymentsToCheck := []DeploymentCheck{
			{
				Namespace:      CSONamespace,
				Name:           "cluster-storage-operator",
				Platform:       "all",
				RequiredLabels: CSOOperatorRequiredLabels,
			},
			{
				Namespace:      CSONamespace,
				Name:           "vsphere-problem-detector-operator",
				Platform:       "vsphere",
				RequiredLabels: CSOOperatorRequiredLabels,
			},
			{
				Namespace:      CSONamespace,
				Name:           "csi-snapshot-controller-operator",
				Platform:       "all",
				RequiredLabels: CSOOperatorRequiredLabels,
			},
			{
				Namespace:      CSONamespace,
				Name:           "csi-snapshot-controller",
				Platform:       "all",
				RequiredLabels: CSOControllerRequiredLabels,
			},
		}
		runDeploymentChecks(kubeClient, CSODeploymentsToCheck, currentPlatform)
	})

	g.It("should verify required labels for CSI related Operators", func() {
		CSIdeploymentsToCheck := []DeploymentCheck{
			{
				Namespace:      CSINamespace,
				Name:           "aws-ebs-csi-driver-operator",
				Platform:       "aws",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "aws-ebs-csi-driver-controller",
				Platform:       "aws",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "aws-efs-csi-driver-operator",
				Platform:       "aws",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "azure-disk-csi-driver-operator",
				Platform:       "azure",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "azure-disk-csi-driver-controller",
				Platform:       "azure",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "azure-file-csi-driver-operator",
				Platform:       "azure",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "azure-file-csi-driver-controller",
				Platform:       "azure",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "gcp-pd-csi-driver-operator",
				Platform:       "gcp",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "gcp-filestore-csi-driver-operator",
				Platform:       "gcp",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "vmware-vsphere-csi-driver-operator",
				Platform:       "vsphere",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "vmware-vsphere-csi-driver-controller",
				Platform:       "vsphere",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "ibm-vpc-block-csi-driver-operator",
				Platform:       "ibmcloud",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "ibm-vpc-block-csi-controller",
				Platform:       "ibmcloud",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "openstack-cinder-csi-driver-operator",
				Platform:       "openstack",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "openstack-cinder-csi-driver-controller",
				Platform:       "openstack",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "manila-csi-driver-operator",
				Platform:       "openstack",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      ManilaCSINamespace,
				Name:           "openstack-manila-csi-controllerplugin",
				Platform:       "openstack",
				RequiredLabels: CSIControllerRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "smb-csi-driver-operator",
				Platform:       "all",
				RequiredLabels: CSIOperatorRequiredLabels,
			},
			{
				Namespace:      CSINamespace,
				Name:           "smb-csi-driver-controller",
				Platform:       "all",
				RequiredLabels: CSIControllerRequiredLabels,
			},
		}
		runDeploymentChecks(kubeClient, CSIdeploymentsToCheck, currentPlatform)
	})

	g.It("should verify required pod-selector for networkpolicy in openshift-cluster-storage-operator namespace", func() {
		npList, err := kubeClient.NetworkingV1().NetworkPolicies(CSONamespace).List(context.TODO(), metav1.ListOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		checkNetworkPolicyPodSelectors(npList.Items, NPCSORequiredLabels)

	})

	g.It("should verify required pod-selector for networkpolicy in openshift-cluster-csi-drivers namespace", func() {
		npList, err := kubeClient.NetworkingV1().NetworkPolicies(CSINamespace).List(context.TODO(), metav1.ListOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		checkNetworkPolicyPodSelectors(npList.Items, NPCSIRequiredLabels)

	})
})

func runDeploymentChecks(clientset *kubernetes.Clientset, deployments []DeploymentCheck, currentPlatform string) {
	results := []string{}
	hasFail := false

	for _, dep := range deployments {
		if dep.Platform != "" && dep.Platform != currentPlatform && dep.Platform != "all" {
			results = append(results, fmt.Sprintf("[SKIP] %s/%s (platform mismatch: %s)", dep.Namespace, dep.Name, dep.Platform))
			continue
		}

		deployment, err := clientset.AppsV1().Deployments(dep.Namespace).Get(context.TODO(), dep.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				results = append(results, fmt.Sprintf("[SKIP] %s/%s not found", dep.Namespace, dep.Name))
				continue
			}
			g.Fail(fmt.Sprintf("Error fetching deployment %s/%s: %v", dep.Namespace, dep.Name, err))
		}

		missingLabels := []string{}
		for key, val := range dep.RequiredLabels {
			if deployment.Spec.Template.Labels[key] != val {
				missingLabels = append(missingLabels, fmt.Sprintf("%s=%s", key, val))
			}
		}

		if len(missingLabels) > 0 {
			results = append(results, fmt.Sprintf("[FAIL] %s/%s missing labels: %s", dep.Namespace, dep.Name, strings.Join(missingLabels, ", ")))
			hasFail = true
		} else {
			results = append(results, fmt.Sprintf("[PASS] %s/%s", dep.Namespace, dep.Name))
		}
	}

	if hasFail {
		summary := strings.Join(results, "\n")
		g.Fail(fmt.Sprintf("Some deployments are missing required labels:\n\n%s", summary))
	}
}

func matchesPodSelector(np networkingv1.NetworkPolicy, key, val string) bool {
	if np.Spec.PodSelector.MatchLabels == nil {
		return false
	}
	return np.Spec.PodSelector.MatchLabels[key] == val
}

func checkNetworkPolicyPodSelectors(npList []networkingv1.NetworkPolicy, requiredLabels map[string]string) {
	for key, val := range requiredLabels {
		found := false
		for _, np := range npList {
			if matchesPodSelector(np, key, val) {
				found = true
				break
			}
		}
		o.Expect(found).To(o.BeTrue(), fmt.Sprintf("missing NetworkPolicy with label %s=%s", key, val))
	}
}
