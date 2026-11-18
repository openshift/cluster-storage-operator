package operator_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	rbacv1 "k8s.io/api/rbac/v1"
)

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
