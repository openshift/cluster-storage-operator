package volumedatasourcevalidator

import (
	"os"
	"testing"

	"github.com/openshift/cluster-storage-operator/assets"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func TestClusterRoleOwnership(t *testing.T) {
	manifest, err := os.ReadFile("../../../manifests/0008_04_volume_data_source_validator_clusterrole.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cvoRole rbacv1.ClusterRole
	if err := yaml.UnmarshalStrict(manifest, &cvoRole); err != nil {
		t.Fatal(err)
	}
	for _, topology := range []string{"hypershift", "ibm-cloud-managed", "self-managed-high-availability", "single-node-developer"} {
		if cvoRole.Annotations["include.release.openshift.io/"+topology] != "true" {
			t.Errorf("CVO ClusterRole missing topology %s", topology)
		}
	}
	bindings := 0
	for _, file := range staticAssets() {
		data, err := assets.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var kind metav1.TypeMeta
		// Validate duplicate keys without rejecting unrelated manifest fields.
		if _, err := yaml.YAMLToJSONStrict(data); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, &kind); err != nil {
			t.Fatal(err)
		}
		switch kind.Kind {
		case "Role", "ClusterRole":
			t.Errorf("%s must be supplied by CVO in both modes", file)
		case "ClusterRoleBinding":
			bindings++
			var binding rbacv1.ClusterRoleBinding
			if err := yaml.UnmarshalStrict(data, &binding); err != nil {
				t.Fatal(err)
			}
			if binding.RoleRef.Kind != "ClusterRole" || binding.RoleRef.Name != cvoRole.Name {
				t.Errorf("binding refers to %v, want ClusterRole %s", binding.RoleRef, cvoRole.Name)
			}
		}
	}
	if bindings != 1 {
		t.Errorf("ClusterRoleBindings = %d, want 1", bindings)
	}
}
