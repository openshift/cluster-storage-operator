package e2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"

	authv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	storagePathAlertLabel = "StoragePathAlert"
	alertTestNamespace    = "storage-path-alerts-"
	defaultExporterNS     = "openshift-cluster-storage-operator"
	defaultPrometheusNS   = "openshift-monitoring"
	defaultRuleName       = "csi-volume-path-health"
	defaultExporterDS     = "volume-device-exporter"
	defaultISCSIClass     = "ci-netapp-iscsi"
	defaultISCSILUKSClass = "ci-netapp-iscsi-luks"
	defaultNVMeClass      = "ci-netapp-nvme"
	defaultNVMeLUKSClass  = "ci-netapp-nvme-luks"
	alertPollInterval     = 5 * time.Second
)

type storagePathAlertConfig struct {
	iscsiStorageClass     string
	iscsiLUKSStorageClass string
	nvmeStorageClass      string
	nvmeLUKSStorageClass  string
	exporterNamespace     string
	exporterDaemonSet     string
	prometheusNamespace   string
	prometheusRule        string
}

type storagePathAlertEnv struct {
	kube       kubernetes.Interface
	dynamic    dynamic.Interface
	httpClient *http.Client
	promURL    string
	promToken  string
	namespace  string
	settings   storagePathAlertConfig
}

type storagePathCase struct {
	id, protocol, storageClass, volumeMode, failedPaths, alert, severity string
}

type prometheusAlert struct {
	Labels map[string]string `json:"labels"`
	State  string            `json:"state"`
}

// CSI storage-path alert end-to-end tests.
//
// Purpose:
// Validate the CSO-delivered storage-path alert contract as an external
// consumer. The suite uses only Kubernetes, Prometheus, and node-debug APIs;
// it does not import CSO controller or asset packages. The tested cluster can
// run an implementation from another branch or payload.
//
// Preconditions:
//   - A disposable cluster has the exporter DaemonSet and
//     PrometheusRule/csi-volume-path-health deployed.
//   - Platform Prometheus is accessible through the prometheus-k8s route.
//   - The caller can create test workloads and short-lived Prometheus tokens.
//   - oc is in PATH for targeted host firewall fault injection.
//   - iSCSI portals use IPv4 and TCP port 3260, as required by the firewall helpers.
//   - KUBECONFIG identifies the prepared test cluster.
//   - These optional variables override the FSxN/Trident StorageClass defaults:
//     ALERT_TEST_ISCSI_STORAGECLASS,
//     ALERT_TEST_ISCSI_LUKS_STORAGECLASS,
//     ALERT_TEST_NVME_STORAGECLASS, and
//     ALERT_TEST_NVME_LUKS_STORAGECLASS.
//     Discover candidate values with:
//     oc get storageclass
//     Confirm each class provisions the expected protocol and encryption mode
//     from its Trident backend and StorageClass parameters.
//     Cases whose configured StorageClass is absent are skipped before checking
//     the exporter or creating workloads. An existing class is expected to
//     provide multipath storage; provisioning and path discovery failures fail
//     the test rather than skip it.
//   - These optional variables use the shown defaults and only need setting
//     when the target cluster differs:
//     ALERT_TEST_EXPORTER_NAMESPACE=openshift-cluster-storage-operator
//     ALERT_TEST_EXPORTER_DAEMONSET=volume-device-exporter
//     ALERT_TEST_PROMETHEUS_NAMESPACE=openshift-monitoring
//     ALERT_TEST_PROMETHEUS_RULE=csi-volume-path-health
//     Discover their values with:
//     oc get daemonset -A | grep volume-device-exporter
//     oc get prometheusrule -A | grep csi-volume-path-health
//     oc get route -n openshift-monitoring prometheus-k8s
//
// Example:
//
//	export KUBECONFIG=/path/to/cluster.kubeconfig
//	export ALERT_TEST_ISCSI_STORAGECLASS=other-iscsi-class
//	export ALERT_TEST_ISCSI_LUKS_STORAGECLASS=other-iscsi-luks-class
//	export ALERT_TEST_NVME_STORAGECLASS=other-nvme-class
//	export ALERT_TEST_NVME_LUKS_STORAGECLASS=other-nvme-luks-class
//
// Execution:
// Run the cso/storage-path-alerting OTE suite with a JUnit output path. The
// suite is serial and disruptive. It runs T1-T8 for filesystem and raw-block
// volumes, then T9 lifecycle coverage. Every injected firewall rule is
// registered for restoration before the fault is applied. Cleanup deletes the
// test namespace and waits for it to disappear. Volume detachment and PV
// reclamation are left to normal Kubernetes and CSI reconciliation.
var _ = g.Describe("[sig-storage][OCPFeatureGate:StoragePathAlert][Serial] CSI storage path alerting", g.Serial, g.Label(storagePathAlertLabel), g.Label("OCPFeatureGate:"+storagePathAlertLabel), g.Label("Serial"), func() {
	var env *storagePathAlertEnv

	g.BeforeEach(func(ctx context.Context) {
		settings, err := storagePathAlertSettings()
		if err != nil {
			g.Skip(err.Error())
		}
		if _, err := exec.LookPath("oc"); err != nil {
			g.Skip(fmt.Sprintf("oc is required for controlled node fault injection: %v", err))
		}

		config, err := newClientConfigForTest()
		if err != nil {
			g.Fail(fmt.Sprintf("load cluster config: %v", err))
		}
		kube, err := kubernetes.NewForConfig(config)
		if err != nil {
			g.Fail(fmt.Sprintf("create Kubernetes client: %v", err))
		}
		dyn, err := dynamic.NewForConfig(config)
		if err != nil {
			g.Fail(fmt.Sprintf("create dynamic client: %v", err))
		}
		env = &storagePathAlertEnv{
			kube:       kube,
			dynamic:    dyn,
			httpClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}, // Prometheus route certificate is cluster-local.
			settings:   settings,
		}
	})

	for _, tc := range []storagePathCase{
		{"T1", "iscsi", "iscsi", "Filesystem", "one", "CSIAddonsVolumeMultipathDegraded", "warning"},
		{"T1", "iscsi", "iscsi", "Block", "one", "CSIAddonsVolumeMultipathDegraded", "warning"},
		{"T2", "iscsi", "iscsi", "Filesystem", "all", "CSIAddonsVolumeMultipathLost", "critical"},
		{"T2", "iscsi", "iscsi", "Block", "all", "CSIAddonsVolumeMultipathLost", "critical"},
		{"T3", "iscsi", "iscsi-luks", "Filesystem", "one", "CSIAddonsVolumeMultipathDegraded", "warning"},
		{"T3", "iscsi", "iscsi-luks", "Block", "one", "CSIAddonsVolumeMultipathDegraded", "warning"},
		{"T4", "iscsi", "iscsi-luks", "Filesystem", "all", "CSIAddonsVolumeMultipathLost", "critical"},
		{"T4", "iscsi", "iscsi-luks", "Block", "all", "CSIAddonsVolumeMultipathLost", "critical"},
		{"T5", "nvme", "nvme", "Filesystem", "one", "CSIAddonsVolumeNVMeSubsystemDegraded", "warning"},
		{"T5", "nvme", "nvme", "Block", "one", "CSIAddonsVolumeNVMeSubsystemDegraded", "warning"},
		{"T6", "nvme", "nvme", "Filesystem", "all", "CSIAddonsVolumeNVMeSubsystemLost", "critical"},
		{"T6", "nvme", "nvme", "Block", "all", "CSIAddonsVolumeNVMeSubsystemLost", "critical"},
		{"T7", "nvme", "nvme-luks", "Filesystem", "one", "CSIAddonsVolumeNVMeSubsystemDegraded", "warning"},
		{"T7", "nvme", "nvme-luks", "Block", "one", "CSIAddonsVolumeNVMeSubsystemDegraded", "warning"},
		{"T8", "nvme", "nvme-luks", "Filesystem", "all", "CSIAddonsVolumeNVMeSubsystemLost", "critical"},
		{"T8", "nvme", "nvme-luks", "Block", "all", "CSIAddonsVolumeNVMeSubsystemLost", "critical"},
	} {
		tc := tc
		g.It(fmt.Sprintf("%s %s %s %s path failure", tc.id, tc.protocol, tc.volumeMode, tc.failedPaths), func(ctx context.Context) {
			env.runPathAlertCase(ctx, tc)
		})
	}

	g.It("T9 removes stale context after detach, PVC deletion, and exporter restart", func(ctx context.Context) {
		env.runLifecycleCase(ctx)
	})
})

func storagePathAlertSettings() (storagePathAlertConfig, error) {
	settings := storagePathAlertConfig{
		iscsiStorageClass:     envOrDefault("ALERT_TEST_ISCSI_STORAGECLASS", defaultISCSIClass),
		iscsiLUKSStorageClass: envOrDefault("ALERT_TEST_ISCSI_LUKS_STORAGECLASS", defaultISCSILUKSClass),
		nvmeStorageClass:      envOrDefault("ALERT_TEST_NVME_STORAGECLASS", defaultNVMeClass),
		nvmeLUKSStorageClass:  envOrDefault("ALERT_TEST_NVME_LUKS_STORAGECLASS", defaultNVMeLUKSClass),
		exporterNamespace:     envOrDefault("ALERT_TEST_EXPORTER_NAMESPACE", defaultExporterNS),
		exporterDaemonSet:     envOrDefault("ALERT_TEST_EXPORTER_DAEMONSET", defaultExporterDS),
		prometheusNamespace:   envOrDefault("ALERT_TEST_PROMETHEUS_NAMESPACE", defaultPrometheusNS),
		prometheusRule:        envOrDefault("ALERT_TEST_PROMETHEUS_RULE", defaultRuleName),
	}
	return settings, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (env *storagePathAlertEnv) storageClassAvailable(ctx context.Context, name string) (bool, error) {
	_, err := env.kube.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get multipath test StorageClass %q: %w", name, err)
	}
	return true, nil
}

func (env *storagePathAlertEnv) setup(ctx context.Context, storageClass string) {
	g.GinkgoHelper()
	available, err := env.storageClassAvailable(ctx, storageClass)
	if err != nil {
		g.Fail(err.Error())
	}
	if !available {
		g.Skip(fmt.Sprintf("multipath test StorageClass %q is not available; prepare a multipath backend or configure the corresponding ALERT_TEST_*_STORAGECLASS override", storageClass))
	}
	if err := env.preflight(ctx); err != nil {
		g.Fail(err.Error())
	}
	g.By("Creating the storage path alert test namespace")
	ns, err := env.kube.CoreV1().Namespaces().Create(ctx, &v1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: alertTestNamespace}}, metav1.CreateOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("create test namespace: %v", err))
	}
	env.namespace = ns.Name
	g.DeferCleanup(func(ctx context.Context) {
		if g.CurrentSpecReport().Failed() {
			env.dumpStorageDiagnostics(ctx)
		}
		if err := env.cleanupNamespace(ctx); err != nil {
			env.dumpStorageDiagnostics(ctx)
			g.Fail(fmt.Sprintf("clean up test storage: %v", err))
		}
	})
}

func (env *storagePathAlertEnv) preflight(ctx context.Context) error {
	g.By("Checking exporter and Prometheus prerequisites")
	if _, err := env.kube.AppsV1().DaemonSets(env.settings.exporterNamespace).Get(ctx, env.settings.exporterDaemonSet, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("get exporter DaemonSet: %w", err)
	}
	rules := schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"}
	if _, err := env.dynamic.Resource(rules).Namespace(env.settings.exporterNamespace).Get(ctx, env.settings.prometheusRule, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("get storage path PrometheusRule: %w", err)
	}
	routes := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	route, err := env.dynamic.Resource(routes).Namespace(env.settings.prometheusNamespace).Get(ctx, "prometheus-k8s", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get Prometheus route: %w", err)
	}
	host, found, err := unstructured.NestedString(route.Object, "spec", "host")
	if err != nil {
		return fmt.Errorf("read Prometheus route host: %w", err)
	}
	if !found || host == "" {
		return fmt.Errorf("Prometheus route has no host")
	}
	env.promURL = "https://" + host
	token, err := env.kube.CoreV1().ServiceAccounts(env.settings.prometheusNamespace).CreateToken(ctx, "prometheus-k8s", &authv1.TokenRequest{Spec: authv1.TokenRequestSpec{ExpirationSeconds: int64Ptr(600)}}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create Prometheus token: %w", err)
	}
	env.promToken = token.Status.Token
	return env.requireAlertRules(ctx)
}

func int64Ptr(value int64) *int64 { return &value }

func (env *storagePathAlertEnv) requireAlertRules(ctx context.Context) error {
	response, err := env.prometheusRequest(ctx, "/api/v1/rules")
	if err != nil {
		return fmt.Errorf("query Prometheus rules: %w", err)
	}
	defer response.Body.Close()
	var rules struct {
		Data struct {
			Groups []struct {
				Rules []struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&rules); err != nil {
		return fmt.Errorf("decode Prometheus rules: %w", err)
	}
	found := map[string]bool{}
	for _, group := range rules.Data.Groups {
		for _, rule := range group.Rules {
			if rule.Type == "alerting" {
				found[rule.Name] = true
			}
		}
	}
	for _, name := range []string{"CSIAddonsVolumeMultipathDegraded", "CSIAddonsVolumeMultipathLost", "CSIAddonsVolumeNVMeSubsystemDegraded", "CSIAddonsVolumeNVMeSubsystemLost", "CSIAddonsVolumeDeviceExporterDown", "CSIAddonsVolumeDeviceExporterNodeDown"} {
		if !found[name] {
			return fmt.Errorf("Prometheus has not loaded required alert rule %s", name)
		}
	}
	return nil
}

func (env *storagePathAlertEnv) runPathAlertCase(ctx context.Context, tc storagePathCase) {
	name := strings.ToLower(fmt.Sprintf("%s-%s", tc.id, tc.volumeMode))
	storageClass := env.storageClass(tc.storageClass)
	env.setup(ctx, storageClass)
	g.By(fmt.Sprintf("Creating %s workload with StorageClass %s", tc.id, storageClass))
	pvc, pod, err := env.createWorkload(ctx, name, storageClass, tc.volumeMode)
	if err != nil {
		g.Fail(fmt.Sprintf("create workload: %v", err))
	}
	pv, err := env.kube.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("get CSI PV: %v", err))
	}
	if pv.Spec.CSI == nil {
		g.Fail(fmt.Sprintf("PV %s is not a CSI volume", pv.Name))
	}
	device, err := env.waitForDevice(ctx, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName)
	if err != nil {
		g.Fail(err.Error())
	}
	lifs, err := env.discoverLIFs(ctx, tc.protocol, pod.Spec.NodeName, device)
	if err != nil {
		g.Fail(fmt.Sprintf("discover independent %s LIFs for %s: %v", tc.protocol, device, err))
	}
	if len(lifs) < 2 {
		g.Fail(fmt.Sprintf("expected at least two independent %s LIFs for %s, found %v", tc.protocol, device, lifs))
	}
	if tc.failedPaths == "one" {
		lifs = lifs[:1]
	}
	g.DeferCleanup(func(ctx context.Context) {
		var failures []string
		for _, lif := range lifs {
			if err := env.restoreLIF(ctx, pod.Spec.NodeName, tc.protocol, lif); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", lif, err))
			}
		}
		if len(failures) > 0 {
			g.Fail("restore paths: " + strings.Join(failures, "; "))
		}
	})
	for _, lif := range lifs {
		g.By(fmt.Sprintf("Blocking %s path %s on %s", tc.protocol, lif, pod.Spec.NodeName))
		if err := env.blockLIF(ctx, pod.Spec.NodeName, tc.protocol, lif); err != nil {
			g.Fail(fmt.Sprintf("block path %s: %v", lif, err))
		}
	}
	g.By(fmt.Sprintf("Waiting for pending or firing %s alert", tc.alert))
	if err := env.waitForAlert(ctx, tc.alert, tc.severity, pvc.Name, pv.Name, pod.Spec.NodeName, "pending", 3*time.Minute); err != nil {
		g.Fail(err.Error())
	}
	// Pending is sufficient to validate the alert expression and volume labels.
	// Also accept Firing if the alert progressed before a successful poll.
	// Do not wait for Firing: the alert hold time adds about five minutes per
	// case (roughly 80 minutes across the suite), exceeding the CI time budget.
	for _, lif := range lifs {
		g.By(fmt.Sprintf("Restoring %s path %s on %s", tc.protocol, lif, pod.Spec.NodeName))
		if err := env.restoreLIF(ctx, pod.Spec.NodeName, tc.protocol, lif); err != nil {
			g.Fail(fmt.Sprintf("restore path %s: %v", lif, err))
		}
	}
	g.By(fmt.Sprintf("Waiting for %s alert to resolve", tc.alert))
	if err := env.waitForAlertResolution(ctx, tc.alert, pvc.Name, 5*time.Minute); err != nil {
		g.Fail(err.Error())
	}
}

func (env *storagePathAlertEnv) runLifecycleCase(ctx context.Context) {
	env.setup(ctx, env.settings.iscsiStorageClass)
	pvc, pod, err := env.createWorkload(ctx, "t9-filesystem", env.settings.iscsiStorageClass, "Filesystem")
	if err != nil {
		g.Fail(fmt.Sprintf("create lifecycle workload: %v", err))
	}
	pv, err := env.kube.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("get lifecycle PV: %v", err))
	}
	if pv.Spec.CSI == nil {
		g.Fail(fmt.Sprintf("lifecycle PV %s is not a CSI volume", pv.Name))
	}
	if _, err := env.waitForDevice(ctx, pv.Spec.CSI.VolumeHandle, pod.Spec.NodeName); err != nil {
		g.Fail(err.Error())
	}
	g.By("Restarting the volume device exporter")
	if output, err := env.runCommand(ctx, "rollout", "restart", "daemonset/"+env.settings.exporterDaemonSet, "-n", env.settings.exporterNamespace); err != nil {
		g.Fail(fmt.Sprintf("restart exporter: %v: %s", err, output))
	}
	g.By("Waiting for the volume device exporter rollout")
	if output, err := env.runCommand(ctx, "rollout", "status", "daemonset/"+env.settings.exporterDaemonSet, "-n", env.settings.exporterNamespace, "--timeout=10m"); err != nil {
		g.Fail(fmt.Sprintf("wait for exporter restart: %v: %s", err, output))
	}
	g.By(fmt.Sprintf("Deleting lifecycle Pod %s/%s", env.namespace, pod.Name))
	if err := env.kube.CoreV1().Pods(env.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
		g.Fail(fmt.Sprintf("delete lifecycle pod: %v", err))
	}
	g.By(fmt.Sprintf("Deleting lifecycle PVC %s/%s", env.namespace, pvc.Name))
	if err := env.kube.CoreV1().PersistentVolumeClaims(env.namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
		g.Fail(fmt.Sprintf("delete lifecycle PVC: %v", err))
	}
	if err := env.waitForMappingRemoval(ctx, pv.Spec.CSI.VolumeHandle, 5*time.Minute); err != nil {
		g.Fail(err.Error())
	}
	if err := env.waitForAlertResolution(ctx, "CSIAddonsVolumeMultipathDegraded", pvc.Name, 10*time.Second); err != nil {
		g.Fail(fmt.Sprintf("unexpected alert CSIAddonsVolumeMultipathDegraded for PVC %s: %v", pvc.Name, err))
	}
	if err := env.waitForAlertResolution(ctx, "CSIAddonsVolumeMultipathLost", pvc.Name, 10*time.Second); err != nil {
		g.Fail(fmt.Sprintf("unexpected alert CSIAddonsVolumeMultipathLost for PVC %s: %v", pvc.Name, err))
	}
	replacementPVC, replacementPod, err := env.createWorkload(ctx, "t9-replacement", env.settings.iscsiStorageClass, "Filesystem")
	if err != nil {
		g.Fail(fmt.Sprintf("create replacement workload: %v", err))
	}
	replacementPV, err := env.kube.CoreV1().PersistentVolumes().Get(ctx, replacementPVC.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		g.Fail(fmt.Sprintf("get replacement PV: %v", err))
	}
	if replacementPV.Spec.CSI == nil {
		g.Fail(fmt.Sprintf("replacement PV %s is not a CSI volume", replacementPV.Name))
	}
	if replacementPV.Spec.CSI.VolumeHandle == pv.Spec.CSI.VolumeHandle {
		g.Fail("replacement workload reused the deleted CSI volume handle")
	}
	if _, err := env.waitForDevice(ctx, replacementPV.Spec.CSI.VolumeHandle, replacementPod.Spec.NodeName); err != nil {
		g.Fail(err.Error())
	}
}

func (env *storagePathAlertEnv) cleanupNamespace(ctx context.Context) error {
	return deleteNamespace(ctx, env.kube, env.namespace)
}

func (env *storagePathAlertEnv) storageClass(kind string) string {
	mapByKind := map[string]string{"iscsi": env.settings.iscsiStorageClass, "iscsi-luks": env.settings.iscsiLUKSStorageClass, "nvme": env.settings.nvmeStorageClass, "nvme-luks": env.settings.nvmeLUKSStorageClass}
	return mapByKind[kind]
}

func (env *storagePathAlertEnv) createWorkload(ctx context.Context, name, storageClass, volumeMode string) (*v1.PersistentVolumeClaim, *v1.Pod, error) {
	g.By(fmt.Sprintf("Creating PVC %s/%s with StorageClass %s and volume mode %s", env.namespace, name, storageClass, volumeMode))
	pvc, err := env.kube.CoreV1().PersistentVolumeClaims(env.namespace).Create(ctx, &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			StorageClassName: &storageClass,
			VolumeMode:       volumeModePtr(v1.PersistentVolumeMode(volumeMode)),
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceStorage: resourceMustParse("1Gi"),
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("create PVC %s/%s: %w", env.namespace, name, err)
	}
	pod := testStoragePathPod(env.namespace, name, volumeMode)
	g.By(fmt.Sprintf("Creating Pod %s/%s", env.namespace, name))
	pod, err = env.kube.CoreV1().Pods(env.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("create Pod %s/%s: %w", env.namespace, name, err)
	}
	g.By(fmt.Sprintf("Waiting for Pod %s/%s to be Running", env.namespace, name))
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := env.kube.CoreV1().Pods(env.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get Pod %s/%s: %w", env.namespace, name, err)
		}
		pod = current
		if current.Status.Phase != v1.PodRunning {
			g.GinkgoLogr.Info("Pod is not Running yet", "namespace", env.namespace, "pod", name,
				"phase", current.Status.Phase, "reason", current.Status.Reason, "message", current.Status.Message,
				"conditions", current.Status.Conditions, "initContainerStatuses", current.Status.InitContainerStatuses,
				"containerStatuses", current.Status.ContainerStatuses)
		}
		return current.Status.Phase == v1.PodRunning, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("wait for Pod %s/%s to be Running (last phase %s, reason %q, message %q, conditions %+v, init container statuses %+v, container statuses %+v): %w",
			env.namespace, name, pod.Status.Phase, pod.Status.Reason, pod.Status.Message, pod.Status.Conditions, pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses, err)
	}
	g.By(fmt.Sprintf("Reading the bound PV for PVC %s/%s (Pod node %s)", env.namespace, name, pod.Spec.NodeName))
	pvc, err = env.kube.CoreV1().PersistentVolumeClaims(env.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("get bound PVC %s/%s: %w", env.namespace, name, err)
	}
	if pvc.Spec.VolumeName == "" {
		return nil, nil, fmt.Errorf("PVC %s is running in Pod %s without a bound PV", pvc.Name, pod.Name)
	}
	return pvc, pod, nil
}

func volumeModePtr(mode v1.PersistentVolumeMode) *v1.PersistentVolumeMode { return &mode }

func resourceMustParse(value string) resource.Quantity { return resource.MustParse(value) }

func testStoragePathPod(namespace, name, volumeMode string) *v1.Pod {
	trueValue, falseValue := true, false
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			SecurityContext: &v1.PodSecurityContext{
				RunAsNonRoot: &trueValue,
				RunAsUser:    int64Ptr(1001),
				SeccompProfile: &v1.SeccompProfile{
					Type: v1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []v1.Container{{
				Name:    "consumer",
				Image:   "registry.access.redhat.com/ubi9/ubi-minimal",
				Command: []string{"sh", "-c", "sleep 3600"},
				SecurityContext: &v1.SecurityContext{
					AllowPrivilegeEscalation: &falseValue,
					Capabilities: &v1.Capabilities{
						Drop: []v1.Capability{"ALL"},
					},
				},
			}},
			Volumes: []v1.Volume{{
				Name: "volume",
				VolumeSource: v1.VolumeSource{
					PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
						ClaimName: name,
					},
				},
			}},
		},
	}
	if volumeMode == "Block" {
		pod.Spec.Containers[0].VolumeDevices = []v1.VolumeDevice{{Name: "volume", DevicePath: "/dev/volume"}}
	} else {
		pod.Spec.Containers[0].VolumeMounts = []v1.VolumeMount{{Name: "volume", MountPath: "/volume"}}
	}
	return pod
}

func (env *storagePathAlertEnv) waitForDevice(ctx context.Context, volumeHandle, node string) (string, error) {
	g.By(fmt.Sprintf("Waiting for the exporter device mapping for volume %s on node %s", volumeHandle, node))
	var device string
	lastObservation := "no query completed"
	err := wait.PollUntilContextTimeout(ctx, time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		result, err := env.query(ctx, fmt.Sprintf("csiaddons_volume_node_device_info{volume_handle=%q,node=%q}", volumeHandle, node))
		if err != nil {
			g.GinkgoLogr.Error(err, "Query exporter device mapping; retrying", "volumeHandle", volumeHandle, "node", node)
			lastObservation = err.Error()
			return false, nil
		}
		if len(result.Data.Result) == 0 {
			lastObservation = "query returned no device mappings"
			return false, nil
		}
		device = result.Data.Result[0].Metric["device"]
		lastObservation = fmt.Sprintf("first mapping labels: %v", result.Data.Result[0].Metric)
		return device != "", nil
	})
	if err != nil {
		return "", fmt.Errorf("wait for exporter mapping for %s on %s: %w; last observation: %s", volumeHandle, node, err, lastObservation)
	}
	return device, nil
}

// storagePathISCSIDiscoveryScript prints unique portal addresses for the target
// block device, not for every iSCSI session on the node. Walk slaves recursively
// to handle dm-crypt -> multipath -> SCSI stacks, then resolve each leaf's sysfs
// path to its session ID. Read that session's lead connection directly instead
// of parsing human-readable iscsiadm output. The firewall helpers currently
// support only test/e2e/storage_path_alerting.goIPv4 portals on the standard iSCSI port (3260).
// The argument is the block device name.
const storagePathISCSIDiscoveryScript = `
device=$1
case "${device}" in
    ''|*[!a-zA-Z0-9_-]*)
        printf 'Invalid block device name: %s\n' "${device}" >&2
        exit 1
        ;;
esac
declare -A visited
discover() {
    local device=$1 slave has_slaves=false session connection address port
    if [[ -n "${visited[${device}]:-}" ]]; then
        return
    fi
    visited[${device}]=1
    for slave in /sys/block/"${device}"/slaves/*; do
        [[ -e "${slave}" ]] || continue
        has_slaves=true
        discover "${slave##*/}" || return 1
    done
    if "${has_slaves}"; then
        return
    fi
    session=$(readlink -f "/sys/block/${device}" | tr '/' '\n' | grep '^session[0-9]\+$' | tail -n 1)
    [[ -n "${session}" ]] || return 0
    connection="/sys/class/iscsi_connection/connection${session#session}:0"
    if ! IFS= read -r address < "${connection}/address" || ! IFS= read -r port < "${connection}/port"; then
        printf 'Cannot read iSCSI endpoint for %s (%s)\n' "${device}" "${session}" >&2
        return 1
    fi
    if [[ "${port}" != 3260 ]]; then
        printf 'Unsupported iSCSI port %s for %s; expected 3260\n' "${port}" "${session}" >&2
        return 1
    fi
    printf '%s\n' "${address}"
}
addresses=$(discover "${device}")
if [[ -z "${addresses}" ]]; then
    printf 'No iSCSI portals found for block device %s\n' "${device}" >&2
    exit 1
fi
printf '%s\n' "${addresses}" | sort -u
`

func (env *storagePathAlertEnv) discoverLIFs(ctx context.Context, protocol, node, device string) ([]string, error) {
	g.By(fmt.Sprintf("Discovering %s paths for device %s on node %s", protocol, device, node))
	var output string
	var err error
	if protocol == "iscsi" {
		output, err = env.nodeCommand(ctx, node, storagePathISCSIDiscoveryScript, device)
	} else {
		command := fmt.Sprintf(`nvme list-subsys /dev/%s | awk -F'traddr=' '/traddr=/ {split($2, a, /[, ]/); print a[1]}' | sort -u`, device)
		output, err = env.nodeCommand(ctx, node, command)
	}
	if err != nil {
		return nil, err
	}
	if protocol == "iscsi" {
		return parseStoragePathISCSILIFs(output)
	}
	return nonEmptyLines(output), nil
}

func parseStoragePathISCSILIFs(output string) ([]string, error) {
	lifs := nonEmptyLines(output)
	for _, lif := range lifs {
		address, err := netip.ParseAddr(lif)
		if err != nil || !address.Is4() {
			return nil, fmt.Errorf("unsupported iSCSI portal address %q: firewall fault injection requires IPv4", lif)
		}
	}
	return lifs, nil
}

func (env *storagePathAlertEnv) blockLIF(ctx context.Context, node, protocol, lif string) error {
	port := 4420
	if protocol == "iscsi" {
		port = 3260
	}
	_, err := env.nodeCommand(ctx, node, fmt.Sprintf("iptables -C OUTPUT -p tcp -d %s --dport %d -j REJECT 2>/dev/null || iptables -I OUTPUT -p tcp -d %s --dport %d -j REJECT", lif, port, lif, port))
	return err
}

func (env *storagePathAlertEnv) restoreLIF(ctx context.Context, node, protocol, lif string) error {
	g.GinkgoLogr.Info("Restoring storage path", "node", node, "protocol", protocol, "lif", lif)
	port := 4420
	if protocol == "iscsi" {
		port = 3260
	}
	_, err := env.nodeCommand(ctx, node, fmt.Sprintf("while iptables -C OUTPUT -p tcp -d %s --dport %d -j REJECT 2>/dev/null; do iptables -D OUTPUT -p tcp -d %s --dport %d -j REJECT; done", lif, port, lif, port))
	return err
}

func (env *storagePathAlertEnv) nodeCommand(ctx context.Context, node, command string, commandArgs ...string) (string, error) {
	args := []string{"debug", "-n", env.namespace, "--quiet", "node/" + node, "--", "chroot", "/host", "bash", "-ceu", command}
	if len(commandArgs) > 0 {
		// bash -c uses the first argument as $0; script parameters start at $1.
		args = append(args, "--")
		args = append(args, commandArgs...)
	}
	output, err := env.runCommand(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("oc debug node/%s: %w: %s", node, err, output)
	}
	return string(output), nil
}

// runCommand logs every oc command and its output, including successful commands.
// Callers must not pass credentials or other secrets in command arguments.
func (env *storagePathAlertEnv) runCommand(ctx context.Context, args ...string) ([]byte, error) {
	g.GinkgoLogr.Info("Running command", "command", "oc", "args", args)
	output, err := exec.CommandContext(ctx, "oc", args...).CombinedOutput()
	g.GinkgoLogr.Info("Command completed", "command", "oc", "args", args, "error", err)
	if len(output) > 0 {
		g.GinkgoWriter.Printf("oc %s output:\n%s\n", strings.Join(args, " "), output)
	}
	return output, err
}

// Diagnostics are best-effort and use runCommand to log commands and output.
// They must not fail the test or prevent normal cleanup when a request fails.
func (env *storagePathAlertEnv) dumpStorageDiagnostics(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	env.dumpNamespace(ctx, env.namespace)
	env.runDiagnosticCommand(ctx, "get", "pv,volumeattachments", "-o", "wide")
	if env.settings.exporterNamespace != env.namespace {
		env.dumpNamespace(ctx, env.settings.exporterNamespace)
	}
}

// dumpNamespace can be reused for any namespace relevant to a failed test.
func (env *storagePathAlertEnv) dumpNamespace(ctx context.Context, namespace string) {
	if namespace == "" {
		return
	}
	for _, args := range [][]string{
		{"get", "pods,pvc", "-n", namespace, "-o", "wide"},
		{"describe", "pods", "-n", namespace},
		{"describe", "pvc", "-n", namespace},
		{"get", "events", "-n", namespace, "--sort-by=.metadata.creationTimestamp"},
	} {
		env.runDiagnosticCommand(ctx, args...)
	}
	pods := env.runDiagnosticCommand(ctx, "get", "pods", "-n", namespace, "-o", "name")
	for _, pod := range nonEmptyLines(string(pods)) {
		// CombinedOutput may also contain oc warnings; only accept pod names.
		if strings.HasPrefix(pod, "pod/") {
			env.runDiagnosticCommand(ctx, "logs", pod, "-n", namespace,
				"--all-containers", "--tail=200", "--timestamps", "--limit-bytes=131072")
		}
	}
}

func (env *storagePathAlertEnv) runDiagnosticCommand(ctx context.Context, args ...string) []byte {
	if ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := env.runCommand(ctx, args...)
	if err != nil {
		return nil // The command and its error/output have already been logged.
	}
	return output
}

func (env *storagePathAlertEnv) waitForAlert(ctx context.Context, alert, severity, pvc, pv, node, state string, timeout time.Duration) error {
	lastObservation := "no query completed"
	err := wait.PollUntilContextTimeout(ctx, alertPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		alerts, err := env.alerts(ctx)
		if err != nil {
			g.GinkgoLogr.Error(err, "Query alerts while waiting for alert; retrying", "alert", alert, "pvc", pvc)
			lastObservation = err.Error()
			return false, nil
		}
		lastObservation = fmt.Sprintf("no matching alert; observed alerts: %+v", alerts)
		for _, current := range alerts {
			labels := current.Labels
			stateMatches := current.State == state || (state == "pending" && current.State == "firing")
			if stateMatches && labels["alertname"] == alert && labels["severity"] == severity && labels["persistentvolumeclaim"] == pvc && labels["persistentvolume"] == pv && labels["namespace"] == env.namespace && labels["node"] == node && labels["device"] != "" {
				g.By(fmt.Sprintf("Observed %s alert in %s state for PV %s, PVC %s, node %s, device %s", alert, current.State, pv, pvc, node, labels["device"]))
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("wait for %s alert in %s state with severity %s for PVC %s/%s, PV %s on node %s: %w; last observation: %s", alert, state, severity, env.namespace, pvc, pv, node, err, lastObservation)
	}
	return nil
}

func (env *storagePathAlertEnv) waitForAlertResolution(ctx context.Context, alert, pvc string, timeout time.Duration) error {
	lastObservation := "no query completed"
	err := wait.PollUntilContextTimeout(ctx, alertPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		alerts, err := env.alerts(ctx)
		if err != nil {
			g.GinkgoLogr.Error(err, "Query alerts while waiting for resolution; retrying", "alert", alert, "pvc", pvc)
			lastObservation = err.Error()
			return false, nil
		}
		for _, current := range alerts {
			if current.Labels["alertname"] == alert && current.Labels["persistentvolumeclaim"] == pvc {
				lastObservation = fmt.Sprintf("alert still present: %+v", current)
				return false, nil
			}
		}
		g.By(fmt.Sprintf("Confirmed %s alert is resolved for PVC %s", alert, pvc))
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for %s alert to resolve for PVC %s/%s: %w; last observation: %s", alert, env.namespace, pvc, err, lastObservation)
	}
	return nil
}

func (env *storagePathAlertEnv) waitForMappingRemoval(ctx context.Context, volumeHandle string, timeout time.Duration) error {
	g.By(fmt.Sprintf("Waiting for exporter mapping removal for volume %s", volumeHandle))
	lastObservation := "no query completed"
	err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		result, err := env.query(ctx, fmt.Sprintf("csiaddons_volume_node_device_info{volume_handle=%q}", volumeHandle))
		if err != nil {
			g.GinkgoLogr.Error(err, "Query exporter mapping while waiting for removal; retrying", "volumeHandle", volumeHandle)
			lastObservation = err.Error()
			return false, nil
		}
		lastObservation = fmt.Sprintf("remaining mappings: %+v", result.Data.Result)
		return len(result.Data.Result) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("wait for exporter mapping removal for volume %s: %w; last observation: %s", volumeHandle, err, lastObservation)
	}
	return nil
}

type storagePathPrometheusQueryResponse struct {
	Data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
		} `json:"result"`
	} `json:"data"`
}

func (env *storagePathAlertEnv) query(ctx context.Context, query string) (*storagePathPrometheusQueryResponse, error) {
	response, err := env.prometheusRequest(ctx, "/api/v1/query?query="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result storagePathPrometheusQueryResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (env *storagePathAlertEnv) alerts(ctx context.Context) ([]prometheusAlert, error) {
	response, err := env.prometheusRequest(ctx, "/api/v1/alerts")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result struct {
		Data struct {
			Alerts []prometheusAlert `json:"alerts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data.Alerts, nil
}

func (env *storagePathAlertEnv) prometheusRequest(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.promURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+env.promToken)
	response, err := env.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		response.Body.Close()
		return nil, fmt.Errorf("Prometheus %s returned %s", path, response.Status)
	}
	return response, nil
}

func deleteNamespace(ctx context.Context, kube kubernetes.Interface, namespace string) error {
	g.By(fmt.Sprintf("Deleting namespace %s and its Pods and PVCs", namespace))
	err := kube.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %s: %w", namespace, err)
	}
	g.By(fmt.Sprintf("Waiting for namespace %s to be deleted", namespace))
	lastObservation := "no namespace lookup completed"
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			g.GinkgoLogr.Error(err, "Get namespace while waiting for deletion; retrying", "namespace", namespace)
			lastObservation = err.Error()
		} else if err == nil {
			lastObservation = fmt.Sprintf("namespace still exists: phase %s, finalizers %v, conditions %+v", current.Status.Phase, current.Spec.Finalizers, current.Status.Conditions)
		}
		return apierrors.IsNotFound(err), nil
	})
	if err != nil {
		return fmt.Errorf("wait for namespace %s to be deleted: %w; last observation: %s", namespace, err, lastObservation)
	}
	return nil
}

func nonEmptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}
