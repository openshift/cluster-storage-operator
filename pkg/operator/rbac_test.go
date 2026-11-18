package operator_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	operatorNS = "openshift-cluster-storage-operator"
	driverNS   = "openshift-cluster-csi-drivers"
	rbacGroup  = "rbac.authorization.k8s.io"
)

type rbacManifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Rules             []rbacv1.PolicyRule `json:"rules"`
	RoleRef           rbacv1.RoleRef      `json:"roleRef"`
	Subjects          []rbacv1.Subject    `json:"subjects"`
}

type permission struct {
	group, resource, namespace, verb, name string
}

// This evaluates the exact-match resource rules shipped for CSO, not API-server
// admission or inherited cluster policy. The remote CI must exercise RBAC's
// bind checks against a real API server as well.
type standalonePolicy map[string][]rbacv1.PolicyRule

func (p standalonePolicy) allows(request permission) bool {
	for namespace, rules := range p {
		if namespace != "" && namespace != request.namespace {
			continue
		}
		for _, rule := range rules {
			if slices.Contains(rule.APIGroups, request.group) &&
				slices.Contains(rule.Resources, request.resource) &&
				slices.Contains(rule.Verbs, request.verb) &&
				(len(rule.ResourceNames) == 0 || slices.Contains(rule.ResourceNames, request.name)) {
				return true
			}
		}
	}
	return false
}

func readRBAC(t *testing.T, path string) []rbacManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader := yaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	var manifests []rbacManifest
	for {
		document, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		// Strict conversion rejects duplicate YAML keys while allowing the
		// non-RBAC fields that this partial manifest type does not model.
		jsonData, err := sigsyaml.YAMLToJSONStrict(document)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var manifest rbacManifest
		if err := json.Unmarshal(jsonData, &manifest); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		manifests = append(manifests, manifest)
	}
	return manifests
}

func loadStandalonePolicy(t *testing.T) standalonePolicy {
	t.Helper()
	paths, err := filepath.Glob("../../manifests/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]rbacManifest{}
	var bindings []rbacManifest
	var manifests []rbacManifest
	for _, path := range paths {
		manifests = append(manifests, readRBAC(t, path)...)
	}
	for _, manifest := range manifests {
		if manifest.Annotations["include.release.openshift.io/self-managed-high-availability"] != "true" ||
			manifest.Annotations["release.openshift.io/delete"] == "true" {
			continue
		}
		switch manifest.Kind {
		case "Role", "ClusterRole":
			key := manifest.Kind + "/" + manifest.Namespace + "/" + manifest.Name
			if _, found := roles[key]; found {
				t.Fatalf("duplicate standalone role: %s", key)
			}
			roles[key] = manifest
		case "RoleBinding", "ClusterRoleBinding":
			bindings = append(bindings, manifest)
		}
	}
	policy := standalonePolicy{}
	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			matches := subject.Kind == "ServiceAccount" && subject.Name == "cluster-storage-operator" && subject.Namespace == operatorNS ||
				subject.Kind == "User" && subject.Name == "system:serviceaccount:"+operatorNS+":cluster-storage-operator" ||
				subject.Kind == "Group" && slices.Contains([]string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:" + operatorNS}, subject.Name)
			if !matches {
				continue
			}
			namespace := binding.Namespace
			if binding.RoleRef.Kind == "ClusterRole" {
				namespace = ""
			}
			key := binding.RoleRef.Kind + "/" + namespace + "/" + binding.RoleRef.Name
			role, found := roles[key]
			if !found {
				t.Fatalf("CSO binding references missing standalone role %s", key)
			}
			for _, rule := range role.Rules {
				for _, values := range [][]string{rule.APIGroups, rule.Resources, rule.Verbs, rule.ResourceNames} {
					for _, value := range values {
						if strings.Contains(value, "*") {
							t.Fatalf("wildcard in CSO rule: %s: %+v", key, rule)
						}
					}
				}
				if slices.Contains(rule.Verbs, "create") && len(rule.ResourceNames) != 0 {
					t.Fatalf("create cannot be constrained by resourceNames: %s", key)
				}
			}
			policy[binding.Namespace] = append(policy[binding.Namespace], role.Rules...)
		}
	}
	return policy
}

func TestStandaloneRBACBoundaries(t *testing.T) {
	policy := loadStandalonePolicy(t)
	for _, tc := range []struct {
		name string
		permission
		allow bool
	}{
		{"observe storage", permission{"operator.openshift.io", "storages", "", "watch", ""}, true},
		{"observe all storage objects", permission{"operator.openshift.io", "storages", "", "list", ""}, true},
		{"update config", permission{"operator.openshift.io", "storages", "", "update", "cluster"}, true},
		{"apply status", permission{"operator.openshift.io", "storages/status", "", "patch", "cluster"}, true},
		{"other storage object", permission{"operator.openshift.io", "storages", "", "update", "other"}, false},
		{"create CSI CR", permission{"operator.openshift.io", "clustercsidrivers", "", "create", "ebs.csi.aws.com"}, true},
		{"update CSI CR", permission{"operator.openshift.io", "clustercsidrivers", "", "update", "ebs.csi.aws.com"}, false},
		{"update CSI CR status", permission{"operator.openshift.io", "clustercsidrivers/status", "", "update", "ebs.csi.aws.com"}, false},
		{"publish operator status", permission{"config.openshift.io", "clusteroperators/status", "", "update", "storage"}, true},
		{"other operator status", permission{"config.openshift.io", "clusteroperators/status", "", "update", "network"}, false},
		{"create validator CRD", permission{"apiextensions.k8s.io", "customresourcedefinitions", "", "create", "volumepopulators.populator.storage.k8s.io"}, true},
		{"update validator CRD", permission{"apiextensions.k8s.io", "customresourcedefinitions", "", "update", "volumepopulators.populator.storage.k8s.io"}, true},
		{"other CRD", permission{"apiextensions.k8s.io", "customresourcedefinitions", "", "update", "other.example.com"}, false},
		{"monitoring informer", permission{"monitoring.coreos.com", "servicemonitors", "", "list", ""}, true},
		{"monitoring write elsewhere", permission{"monitoring.coreos.com", "servicemonitors", "unrelated", "create", "other"}, false},
		{"driver deployment", permission{"apps", "deployments", driverNS, "create", "aws-ebs-csi-driver-operator"}, true},
		{"validator deployment", permission{"apps", "deployments", operatorNS, "update", "volume-data-source-validator"}, true},
		{"exporter daemonset", permission{"apps", "daemonsets", operatorNS, "create", "csi-volume-device-exporter"}, true},
		{"driver daemonset", permission{"apps", "daemonsets", driverNS, "create", "other"}, false},
		{"deployment elsewhere", permission{"apps", "deployments", "unrelated", "create", "other"}, false},
		{"leader lease", permission{"coordination.k8s.io", "leases", operatorNS, "update", "cluster-storage-operator-lock"}, true},
		{"other lease", permission{"coordination.k8s.io", "leases", operatorNS, "update", "other"}, false},
		{"old config cleanup", permission{"", "configmaps", "openshift-config", "delete", "selinux-conflicts"}, true},
		{"unrelated config deletion", permission{"", "configmaps", "openshift-config", "delete", "other"}, false},
		{"read client CA", permission{"", "configmaps", "kube-system", "get", "extension-apiserver-authentication"}, true},
		{"write client CA", permission{"", "configmaps", "kube-system", "update", "extension-apiserver-authentication"}, false},
		{"operator SCC use", permission{"security.openshift.io", "securitycontextconstraints", operatorNS, "use", "nonroot-v2"}, true},
		{"operator SCC use elsewhere", permission{"security.openshift.io", "securitycontextconstraints", driverNS, "use", "nonroot-v2"}, false},
		{"operator SCC use cluster-wide", permission{"security.openshift.io", "securitycontextconstraints", "", "use", "nonroot-v2"}, false},
		{"privileged SCC use", permission{"security.openshift.io", "securitycontextconstraints", operatorNS, "use", "privileged"}, false},
		{"exporter SCC use", permission{"security.openshift.io", "securitycontextconstraints", operatorNS, "use", "hostmount-anyuid-v2"}, false},
		{"operator SCC update", permission{"security.openshift.io", "securitycontextconstraints", operatorNS, "update", "nonroot-v2"}, false},
		{"operator SCC patch", permission{"security.openshift.io", "securitycontextconstraints", operatorNS, "patch", "nonroot-v2"}, false},
		{"SCC delegation", permission{rbacGroup, "clusterroles", operatorNS, "bind", "system:openshift:scc:hostmount-anyuid-v2"}, true},
		{"SCC delegation elsewhere", permission{rbacGroup, "clusterroles", driverNS, "bind", "system:openshift:scc:hostmount-anyuid-v2"}, false},
		{"legacy privileged SCC delegation", permission{rbacGroup, "clusterroles", operatorNS, "bind", "system:openshift:scc:privileged"}, false},
		{"exporter CA cleanup", permission{"", "configmaps", operatorNS, "delete", "csi-volume-device-exporter-client-ca"}, true},
		{"legacy exporter CA cleanup", permission{"", "configmaps", operatorNS, "delete", "volume-device-exporter-client-ca"}, false},
		{"cluster-admin delegation", permission{rbacGroup, "clusterroles", "", "bind", "cluster-admin"}, false},
		{"cluster role modification", permission{rbacGroup, "clusterroles", "", "update", "aws-ebs-csi-driver-operator-clusterrole"}, false},
		{"cluster role creation", permission{rbacGroup, "clusterroles", "", "create", "other"}, false},
		{"cluster role escalation", permission{rbacGroup, "clusterroles", "", "escalate", "aws-ebs-csi-driver-operator-clusterrole"}, false},
		{"own role escalation", permission{rbacGroup, "roles", operatorNS, "escalate", "cluster-storage-operator"}, false},
		{"unrelated role escalation", permission{rbacGroup, "roles", driverNS, "escalate", "other"}, false},
		{"unrelated role binding", permission{rbacGroup, "roles", driverNS, "bind", "other"}, false},
		{"observe cluster bindings", permission{rbacGroup, "clusterrolebindings", "", "list", ""}, true},
		{"create cluster binding", permission{rbacGroup, "clusterrolebindings", "", "create", ""}, true},
		{"observe CSI bindings", permission{rbacGroup, "rolebindings", driverNS, "watch", ""}, true},
		{"create CSI binding", permission{rbacGroup, "rolebindings", driverNS, "create", ""}, true},
		{"update CSI binding", permission{rbacGroup, "rolebindings", driverNS, "update", "aws-ebs-csi-driver-operator-rolebinding"}, true},
		{"unrelated CSI binding", permission{rbacGroup, "rolebindings", driverNS, "update", "other"}, false},
		{"unrelated binding modification", permission{rbacGroup, "clusterrolebindings", "", "update", "cluster-admin"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.allows(tc.permission); got != tc.allow {
				t.Fatalf("authorization for %+v = %v, want %v", tc.permission, got, tc.allow)
			}
		})
	}
	if policy.allows(permission{"", "secrets", "unrelated", "watch", ""}) {
		t.Error("unexpected Secret informer access outside the five namespaces")
	}
	for _, ns := range []string{operatorNS, driverNS, "openshift-config", "openshift-config-managed", "kube-system"} {
		if !policy.allows(permission{"", "secrets", ns, "watch", ""}) {
			t.Errorf("missing Secret informer access in %s", ns)
		}
		for _, verb := range []string{"create", "update", "patch", "delete"} {
			if policy.allows(permission{"", "secrets", ns, verb, "example"}) {
				t.Errorf("unexpected Secret %s in %s", verb, ns)
			}
		}
		// A Role creation POST has no name in its URL. In particular, check
		// that CSO has no unnamed escalation grant to bypass admission.
		for _, name := range []string{"", "cluster-storage-operator", "other"} {
			for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete", "escalate"} {
				if policy.allows(permission{rbacGroup, "roles", ns, verb, name}) {
					t.Errorf("unexpected Role %s in %s for name %q", verb, ns, name)
				}
			}
		}
	}
}

func TestRolePayloadOrdering(t *testing.T) {
	for _, topology := range []string{"hypershift", "ibm-cloud-managed", "self-managed-high-availability", "single-node-developer"} {
		t.Run(topology, func(t *testing.T) { testRolePayloadOrdering(t, topology) })
	}
}

func testRolePayloadOrdering(t *testing.T, topology string) {
	// oc adm release new prefixes component-local files, but preserves global
	// filenames starting with 0000_. Compare the resulting payload filenames,
	// not the source filenames (the operator Namespace is a global manifest).
	payloadFilename := func(path string) string {
		name := filepath.Base(path)
		if strings.HasPrefix(name, "0000_") {
			return name
		}
		return "0000_50_cluster-storage-operator_" + name
	}
	paths, err := filepath.Glob("../../manifests/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	namespaces := map[string]string{}
	type roleFile struct {
		path string
		role rbacManifest
	}
	var roles []roleFile
	var deployment string
	for _, path := range paths {
		for _, manifest := range readRBAC(t, path) {
			if manifest.Annotations["include.release.openshift.io/"+topology] != "true" ||
				manifest.Annotations["release.openshift.io/delete"] == "true" {
				continue
			}
			switch manifest.Kind {
			case "Namespace":
				namespaces[manifest.Name] = path
			case "Role":
				roles = append(roles, roleFile{path, manifest})
			case "Deployment":
				if manifest.Name == "cluster-storage-operator" && manifest.Namespace == operatorNS {
					deployment = path
				}
			}
		}
	}
	if deployment == "" && topology != "hypershift" || len(roles) == 0 {
		t.Fatal("expected Deployment and Roles (Deployment is CPO-managed in HyperShift)")
	}
	componentRoles := standaloneComponentRoleNamespaces()
	for _, file := range roles {
		// The component Roles are prerequisites for CSO reconciliation. Other
		// Roles, such as CSO's metrics reader at runlevel 90, can follow startup.
		if _, required := componentRoles[file.role.Name]; required && deployment != "" && payloadFilename(file.path) >= payloadFilename(deployment) {
			t.Errorf("Role %s must precede Deployment %s in the payload", file.path, deployment)
		}
		namespace, found := namespaces[file.role.Namespace]
		if !found {
			// These platform namespaces are supplied by other release components.
			// In particular, the AWS configuration Role needs openshift-config-managed.
			if !slices.Contains([]string{"openshift-config", "openshift-config-managed", "kube-system"}, file.role.Namespace) {
				t.Errorf("Role %s has no namespace manifest or declared external prerequisite", file.path)
			}
			continue
		}
		if payloadFilename(namespace) >= payloadFilename(file.path) {
			t.Errorf("Namespace %s must precede Role %s in the payload", namespace, file.path)
		}
	}
}

func standaloneComponentRoleNamespaces() map[string]string {
	return map[string]string{
		"aws-ebs-csi-driver-operator-role":              driverNS,
		"azure-disk-csi-driver-operator-role":           driverNS,
		"azure-file-csi-driver-operator-role":           driverNS,
		"gcp-pd-csi-driver-operator-role":               driverNS,
		"ibm-vpc-block-csi-driver-operator-role":        driverNS,
		"manila-csi-driver-operator-role":               driverNS,
		"openstack-cinder-csi-driver-operator-role":     driverNS,
		"powervs-block-csi-driver-operator-role":        driverNS,
		"vmware-vsphere-csi-driver-operator-role":       driverNS,
		"vmware-vsphere-csi-driver-operator-prometheus": driverNS,
		"aws-ebs-csi-driver-operator-aws-config-role":   "openshift-config-managed",
		"vsphere-problem-detector-operator-role":        operatorNS,
	}
}

// Verify CVO ownership and delegation against the actual assets, so adding a
// binding cannot silently introduce an authorization failure on another cloud.
func TestStandaloneRBACComponentDelegation(t *testing.T) {
	policy := loadStandalonePolicy(t)
	wantRoles := standaloneComponentRoleNamespaces()
	paths, err := filepath.Glob("../../manifests/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]bool{}
	for _, path := range paths {
		for _, role := range readRBAC(t, path) {
			if role.Kind != "Role" || role.Annotations["include.release.openshift.io/self-managed-high-availability"] != "true" {
				continue
			}
			if _, expected := wantRoles[role.Name]; !expected {
				continue
			}
			if wantRoles[role.Name] != role.Namespace {
				t.Fatalf("%s: unexpected component Role %s/%s", path, role.Namespace, role.Name)
			}
			for _, topology := range []string{"ibm-cloud-managed", "self-managed-high-availability", "single-node-developer"} {
				if role.Annotations["include.release.openshift.io/"+topology] != "true" {
					t.Errorf("%s: missing %s annotation", path, topology)
				}
			}
			wantHyperShift := ""
			if role.Name == "powervs-block-csi-driver-operator-role" {
				wantHyperShift = "true"
			}
			if role.Annotations["include.release.openshift.io/hypershift"] != wantHyperShift || role.Annotations["capability.openshift.io/name"] != "Storage" {
				t.Errorf("%s: incorrect topology or capability annotations", path)
			}
			key := role.Namespace + "/" + role.Name
			if roles[key] {
				t.Fatalf("duplicate component Role: %s", key)
			}
			roles[key] = true
			if !policy.allows(permission{rbacGroup, "roles", role.Namespace, "bind", role.Name}) {
				t.Errorf("missing bind on %s", key)
			}
			for _, ns := range []string{"", operatorNS, driverNS, "openshift-config-managed", "unrelated"} {
				if ns != role.Namespace && policy.allows(permission{rbacGroup, "roles", ns, "bind", role.Name}) {
					t.Errorf("unexpected bind on %s in namespace %q", role.Name, ns)
				}
			}
			for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete", "escalate"} {
				if policy.allows(permission{rbacGroup, "roles", role.Namespace, verb, role.Name}) {
					t.Errorf("unexpected %s on CVO-managed Role %s", verb, key)
				}
			}
		}
	}
	if len(roles) != len(wantRoles) {
		t.Fatalf("found %d CVO component Roles, want %d", len(roles), len(wantRoles))
	}
	err = filepath.WalkDir("../../assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "base" || entry.Name() == "hypershift") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".patch.yaml") {
			return nil
		}
		for _, manifest := range readRBAC(t, path) {
			var checks []permission
			switch manifest.Kind {
			case "Role":
				t.Errorf("%s: standalone component Role must be managed by CVO", path)
			case "RoleBinding", "ClusterRoleBinding":
				resource := "roles"
				if manifest.RoleRef.Kind == "ClusterRole" {
					resource = "clusterroles"
				} else if !roles[manifest.Namespace+"/"+manifest.RoleRef.Name] {
					t.Errorf("%s: binding references missing CVO Role %s/%s", path, manifest.Namespace, manifest.RoleRef.Name)
				}
				bindingResource := "rolebindings"
				if manifest.Kind == "ClusterRoleBinding" {
					bindingResource = "clusterrolebindings"
				}
				checks = []permission{
					{rbacGroup, resource, manifest.Namespace, "bind", manifest.RoleRef.Name},
					{rbacGroup, bindingResource, manifest.Namespace, "create", manifest.Name},
					{rbacGroup, bindingResource, manifest.Namespace, "update", manifest.Name},
				}
			}
			for _, request := range checks {
				if !policy.allows(request) {
					t.Errorf("%s: missing permission %+v", path, request)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
