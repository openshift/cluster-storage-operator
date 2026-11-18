package operator_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestCSOPolicyByTopology(t *testing.T) {
	guest := loadOperatorPolicy(t, "hypershift")
	if len(guest) != 6 {
		t.Fatalf("expected cluster permissions and five namespace scopes, got %d", len(guest))
	}
	for _, topology := range []string{"ibm-cloud-managed", "self-managed-high-availability", "single-node-developer"} {
		if standalone := loadOperatorPolicy(t, topology); !reflect.DeepEqual(guest, standalone) {
			t.Errorf("CSO guest authorization differs from %s", topology)
		}
	}
}

func TestCSOBindingsByTopology(t *testing.T) {
	paths, err := filepath.Glob("../../manifests/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, topology := range []string{"hypershift", "ibm-cloud-managed", "self-managed-high-availability", "single-node-developer"} {
		t.Run(topology, func(t *testing.T) {
			want := map[string]rbacv1.RoleRef{
				"": {APIGroup: rbacGroup, Kind: "ClusterRole", Name: "cluster-storage-operator-main-clusterrole"},
			}
			for _, ns := range []string{operatorNS, driverNS, "openshift-config", "openshift-config-managed", "kube-system"} {
				want[ns] = rbacv1.RoleRef{APIGroup: rbacGroup, Kind: "Role", Name: "cluster-storage-operator"}
			}
			var deletedLegacy bool
			for _, path := range paths {
				for _, manifest := range readRBAC(t, path) {
					if manifest.Annotations["include.release.openshift.io/"+topology] != "true" {
						continue
					}
					if manifest.Kind == "ClusterRoleBinding" && manifest.Name == "cluster-storage-operator-role" {
						if manifest.Annotations["release.openshift.io/delete"] != "true" || deletedLegacy {
							t.Errorf("%s: legacy binding must have exactly one deletion manifest", path)
						}
						deletedLegacy = true
					}
					if manifest.Annotations["release.openshift.io/delete"] == "true" ||
						(manifest.Kind != "RoleBinding" && manifest.Kind != "ClusterRoleBinding") {
						continue
					}
					isMain := manifest.Name == "cluster-storage-operator-main-clusterrolebinding"
					if !isMain && manifest.RoleRef.Name != "cluster-storage-operator" {
						continue
					}
					ref, exists := want[manifest.Namespace]
					if !exists || manifest.RoleRef != ref {
						t.Errorf("%s: duplicate or unexpected CSO binding: %+v", path, manifest.RoleRef)
					}
					delete(want, manifest.Namespace)
					kind := "RoleBinding"
					if isMain {
						kind = "ClusterRoleBinding"
					}
					if manifest.Kind != kind || manifest.Annotations["capability.openshift.io/name"] != "Storage" {
						t.Errorf("%s: incorrect binding kind or capability", path)
					}
					if !reflect.DeepEqual(manifest.Subjects, []rbacv1.Subject{{Kind: "ServiceAccount", Name: "cluster-storage-operator", Namespace: operatorNS}}) {
						t.Errorf("%s: unexpected CSO subjects: %+v", path, manifest.Subjects)
					}
				}
			}
			if !deletedLegacy || len(want) != 0 {
				t.Errorf("legacy deletion present = %v; missing CSO bindings: %v", deletedLegacy, want)
			}
		})
	}
	for _, path := range []string{"../../manifests/08_operator_rbac.yaml", "../../manifests/08_operator_rbac-hypershift.yaml", "../../profile-patches/hypershift/08_operator_rbac.yaml-patch"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("obsolete admin binding artifact %s must be absent (stat: %v)", path, err)
		}
	}
}

func TestHyperShiftCSOKubeconfigInjection(t *testing.T) {
	data, err := os.ReadFile("../../manifests/10_deployment-hypershift.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var deployment appsv1.Deployment
	if err := sigsyaml.UnmarshalStrict(data, &deployment); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "cluster-storage-operator" {
			found = true
			if !slices.Contains(container.Args, "--guest-kubeconfig=/etc/guest-kubeconfig/kubeconfig") {
				t.Error("CSO must use the guest kubeconfig injected by CPO")
			}
		}
		for _, mount := range container.VolumeMounts {
			if mount.Name == "guest-kubeconfig" || mount.MountPath == "/etc/guest-kubeconfig" {
				t.Errorf("guest kubeconfig mount must be injected by CPO: %+v", mount)
			}
		}
	}
	if !found {
		t.Fatal("missing CSO container")
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == "guest-kubeconfig" || volume.Secret != nil && volume.Secret.SecretName == "service-network-admin-kubeconfig" {
			t.Errorf("unexpected administrative guest kubeconfig volume: %+v", volume)
		}
	}
}

func roleKey(role rbacManifest) string {
	return role.Kind + "/" + role.Namespace + "/" + role.Name
}

func payloadRoles(t *testing.T, topology string) map[string]rbacManifest {
	t.Helper()
	paths, err := filepath.Glob("../../manifests/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]rbacManifest{}
	for _, path := range paths {
		for _, role := range readRBAC(t, path) {
			if role.Kind != "Role" && role.Kind != "ClusterRole" ||
				role.Annotations["include.release.openshift.io/"+topology] != "true" ||
				role.Annotations["release.openshift.io/delete"] == "true" {
				continue
			}
			key := roleKey(role)
			if _, found := roles[key]; found {
				t.Fatalf("%s: duplicate %s role %s", path, topology, key)
			}
			roles[key] = role
		}
	}
	return roles
}

func TestComponentRBACByTopology(t *testing.T) {
	guestRoles := map[string]bool{
		"aws-ebs-csi-driver-operator-role":          true,
		"azure-disk-csi-driver-operator-role":       true,
		"azure-file-csi-driver-operator-role":       true,
		"gcp-pd-csi-driver-operator-role":           true,
		"openstack-cinder-csi-driver-operator-role": true,
		"powervs-block-csi-driver-operator-role":    true,
	}
	guestClusterRoles := map[string]bool{
		"aws-ebs-csi-driver-operator-clusterrole":          true,
		"azure-disk-csi-driver-operator-clusterrole":       true,
		"azure-file-csi-driver-operator-clusterrole":       true,
		"gcp-pd-csi-driver-operator-clusterrole":           true,
		"manila-csi-driver-operator-clusterrole":           true,
		"openstack-cinder-csi-driver-operator-clusterrole": true,
		"powervs-block-csi-driver-operator-clusterrole":    true,
		"volume-data-source-validator":                     true,
		"csi-volume-device-exporter-auth-proxy":            true,
	}
	componentRoles := standaloneComponentRoleNamespaces()
	isComponent := func(role rbacManifest) bool {
		if role.Kind == "Role" {
			namespace, found := componentRoles[role.Name]
			return found && namespace == role.Namespace
		}
		return guestClusterRoles[role.Name] || slices.Contains([]string{
			"ibm-vpc-block-csi-driver-operator-clusterrole",
			"vmware-vsphere-csi-driver-operator-clusterrole",
			"vsphere-problem-detector-operator-clusterrole",
		}, role.Name)
	}
	guest := payloadRoles(t, "hypershift")
	// Sidecar RBAC and the metrics reader were already shared across topologies.
	// Check uniqueness above for all roles, then compare component ownership.
	for key, role := range guest {
		if !isComponent(role) {
			delete(guest, key)
		}
	}
	for _, topology := range []string{"ibm-cloud-managed", "self-managed-high-availability", "single-node-developer"} {
		standalone := payloadRoles(t, topology)
		for key, role := range standalone {
			if !isComponent(role) {
				continue
			}
			wantGuest := guestClusterRoles[role.Name]
			if role.Kind == "Role" {
				wantGuest = guestRoles[role.Name] && role.Namespace == driverNS
			}
			guestRole, exists := guest[key]
			if exists != wantGuest {
				t.Errorf("%s: guest inclusion for %s = %v, want %v", topology, key, exists, wantGuest)
			}
			if !wantGuest || !exists {
				continue
			}
			if guestRole.Annotations["capability.openshift.io/name"] != "Storage" {
				t.Errorf("%s: missing Storage capability", key)
			}
			// Five guest variants retain the historical PVC grant. All other
			// rules, including PowerVS's existing PVC grant, are shared.
			wantRules := role.Rules
			if role.Kind == "Role" && role.Name != "powervs-block-csi-driver-operator-role" {
				wantRules = append([]rbacv1.PolicyRule(nil), role.Rules...)
				wantRules[0].Resources = append(slices.Clone(role.Rules[0].Resources), "persistentvolumeclaims")
				if slices.Contains(role.Rules[0].Resources, "persistentvolumeclaims") {
					t.Errorf("standalone Role %s unexpectedly gained PVC permissions", key)
				}
				if len(guestRole.Annotations) != 2 {
					t.Errorf("%s: guest variant must only have HyperShift and Storage annotations", key)
				}
			}
			if !reflect.DeepEqual(guestRole.Rules, wantRules) {
				t.Errorf("%s: guest rules differ from standalone plus historical PVC permissions", key)
			}
		}
	}
	if len(guest) != len(guestRoles)+len(guestClusterRoles) {
		t.Errorf("guest has %d Roles/ClusterRoles, want 6/9", len(guest))
	}
}

func TestHyperShiftDriverAssetOwnership(t *testing.T) {
	roles := payloadRoles(t, "hypershift")
	configs := []csioperatorclient.CSIOperatorConfig{
		csioperatorclient.GetAWSEBSCSIOperatorConfig(true),
		csioperatorclient.GetAzureDiskCSIOperatorConfig(true),
		csioperatorclient.GetAzureFileCSIOperatorConfig(true),
		csioperatorclient.GetGCPPDCSIOperatorConfig(true),
		csioperatorclient.GetOpenStackCinderCSIOperatorConfig(true),
		csioperatorclient.GetOpenStackManilaOperatorConfig(true, nil, nil),
		csioperatorclient.GetPowerVSBlockCSIOperatorConfig(true),
	}
	for _, cfg := range configs {
		t.Run(cfg.CSIDriverName, func(t *testing.T) {
			guestBindings, guestAccounts := 0, 0
			for _, asset := range cfg.StaticAssets {
				for _, manifest := range readRBAC(t, filepath.Join("../../assets", asset)) {
					switch manifest.Kind {
					case "Role", "ClusterRole":
						t.Errorf("%s: guest role must be supplied by CVO", asset)
					case "ServiceAccount":
						guestAccounts++
					case "RoleBinding", "ClusterRoleBinding":
						guestBindings++
						namespace := manifest.Namespace
						if manifest.RoleRef.Kind == "ClusterRole" {
							namespace = ""
						}
						key := manifest.RoleRef.Kind + "/" + namespace + "/" + manifest.RoleRef.Name
						if _, found := roles[key]; !found {
							t.Errorf("%s: binding references missing payload role %s", asset, key)
						}
					}
				}
			}
			wantBindings, wantAccounts := 2, 1
			if cfg.CSIDriverName == "manila.csi.openstack.org" {
				wantBindings, wantAccounts = 1, 0
			}
			if guestBindings != wantBindings || guestAccounts != wantAccounts {
				t.Errorf("guest bindings/accounts = %d/%d, want %d/%d", guestBindings, guestAccounts, wantBindings, wantAccounts)
			}
			mgmtRoles := map[string]bool{}
			var mgmtBindings []rbacManifest
			for _, asset := range cfg.MgmtStaticAssets {
				for _, manifest := range readRBAC(t, filepath.Join("../../assets", asset)) {
					if manifest.Kind == "Role" {
						mgmtRoles[manifest.Name] = true
						if manifest.Namespace != "${CONTROLPLANE_NAMESPACE}" {
							t.Errorf("%s: management Role lost dynamic namespace", asset)
						}
					}
					if manifest.Kind == "RoleBinding" {
						mgmtBindings = append(mgmtBindings, manifest)
					}
				}
			}
			if len(mgmtRoles) != 1 || len(mgmtBindings) != 1 {
				t.Errorf("management roles/bindings = %d/%d, want 1/1", len(mgmtRoles), len(mgmtBindings))
			}
			for _, binding := range mgmtBindings {
				if !mgmtRoles[binding.RoleRef.Name] || binding.Namespace != "${CONTROLPLANE_NAMESPACE}" {
					t.Errorf("management binding lost its role or dynamic namespace: %+v", binding)
				}
			}
			// Also check the full generated guest directory: an unused role
			// must not be left behind in the kustomize output.
			paths, err := filepath.Glob(filepath.Join("../../assets", filepath.Dir(cfg.CRAsset), "*.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				for _, manifest := range readRBAC(t, path) {
					if manifest.Kind == "Role" || manifest.Kind == "ClusterRole" {
						t.Errorf("%s: guest output still contains a role", path)
					}
				}
			}
		})
	}
}
