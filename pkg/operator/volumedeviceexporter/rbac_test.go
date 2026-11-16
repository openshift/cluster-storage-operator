package volumedeviceexporter

import (
	"os"
	"reflect"
	"testing"

	"github.com/openshift/cluster-storage-operator/assets"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// Check that the two modes have exactly one owner for each ClusterRole, while
// keeping the role consumed in HyperShift identical to the CVO-supplied role.
func TestClusterRoleOwnership(t *testing.T) {
	manifest, err := os.ReadFile("../../../manifests/0008_04_volume_device_exporter_auth_proxy_clusterrole.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cvoRole rbacv1.ClusterRole
	if err := yaml.Unmarshal(manifest, &cvoRole); err != nil {
		t.Fatal(err)
	}
	for _, isHypershift := range []bool{true, false, true} {
		roles, bindings := 0, 0
		for _, file := range staticAssets(isHypershift) {
			data, err := assets.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var kind metav1.TypeMeta
			if err := yaml.Unmarshal(data, &kind); err != nil {
				t.Fatal(err)
			}
			switch kind.Kind {
			case "ClusterRole":
				roles++
				var role rbacv1.ClusterRole
				if err := yaml.Unmarshal(data, &role); err != nil {
					t.Fatal(err)
				}
				if role.Name != cvoRole.Name || !reflect.DeepEqual(role.Rules, cvoRole.Rules) {
					t.Errorf("HyperShift role differs from CVO role: %s", file)
				}
			case "ClusterRoleBinding":
				bindings++
				var binding rbacv1.ClusterRoleBinding
				if err := yaml.Unmarshal(data, &binding); err != nil {
					t.Fatal(err)
				}
				if binding.RoleRef.Name != cvoRole.Name {
					t.Errorf("binding refers to %q, want %q", binding.RoleRef.Name, cvoRole.Name)
				}
			}
		}
		wantRoles := 0
		if isHypershift {
			wantRoles = 1
		}
		if roles != wantRoles || bindings != 1 {
			t.Errorf("isHypershift=%v: roles=%d, bindings=%d; want %d, 1", isHypershift, roles, bindings, wantRoles)
		}
	}
}
