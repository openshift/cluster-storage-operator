package csidriveroperator

import (
	"context"
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourcehelper"
)

const (
	envProvisionerImage         = "PROVISIONER_IMAGE"
	envAttacherImage            = "ATTACHER_IMAGE"
	envResizerImage             = "RESIZER_IMAGE"
	envSnapshotterImage         = "SNAPSHOTTER_IMAGE"
	envNodeDriverRegistrarImage = "NODE_DRIVER_REGISTRAR_IMAGE"
	envLivenessProbeImage       = "LIVENESS_PROBE_IMAGE"
	envKubeRBACProxyImage       = "KUBE_RBAC_PROXY_IMAGE"
	envToolsImage               = "TOOLS_IMAGE"

	envLivenessProbeControlPlaneImage = "LIVENESS_PROBE_CONTROL_PLANE_IMAGE"
	envKubeRBACProxyControlPlaneImage = "KUBE_RBAC_PROXY_CONTROL_PLANE_IMAGE"
)

var (
	sidecarReplacer = strings.NewReplacer(
		"${PROVISIONER_IMAGE}", os.Getenv(envProvisionerImage),
		"${ATTACHER_IMAGE}", os.Getenv(envAttacherImage),
		"${RESIZER_IMAGE}", os.Getenv(envResizerImage),
		"${SNAPSHOTTER_IMAGE}", os.Getenv(envSnapshotterImage),
		"${NODE_DRIVER_REGISTRAR_IMAGE}", os.Getenv(envNodeDriverRegistrarImage),
		"${LIVENESS_PROBE_IMAGE}", os.Getenv(envLivenessProbeImage),
		"${LIVENESS_PROBE_CONTROL_PLANE_IMAGE}", os.Getenv(envLivenessProbeControlPlaneImage),
		"${KUBE_RBAC_PROXY_IMAGE}", os.Getenv(envKubeRBACProxyImage),
		"${KUBE_RBAC_PROXY_CONTROL_PLANE_IMAGE}", os.Getenv(envKubeRBACProxyControlPlaneImage),
		"${TOOLS_IMAGE}", os.Getenv(envToolsImage),
	)
)

// factory.PostStartHook to poke newly started controller to resync.
// This is useful if a controller is started later than at CSO startup
// - CSO's CR may have been already processes and there may be no
// event pending in its informers.
func initalSync(ctx context.Context, syncContext factory.SyncContext) error {
	syncContext.Queue().Add(factory.DefaultQueueKey)
	return nil
}

func reportCreateEvent(recorder events.Recorder, obj runtime.Object, originalErr error) {
	gvk := resourcehelper.GuessObjectGroupVersionKind(obj)
	if originalErr == nil {
		recorder.Eventf(fmt.Sprintf("%sCreated", gvk.Kind), "Created %s because it was missing", resourcehelper.FormatResourceForCLIWithNamespace(obj))
		return
	}
	recorder.Warningf(fmt.Sprintf("%sCreateFailed", gvk.Kind), "Failed to create %s: %v", resourcehelper.FormatResourceForCLIWithNamespace(obj), originalErr)
}
