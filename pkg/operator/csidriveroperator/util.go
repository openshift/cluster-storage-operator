package csidriveroperator

import (
	"context"
	"os"
	"strings"

	"github.com/openshift/library-go/pkg/controller/factory"
)

const (
	envProvisionerImage         = "PROVISIONER_IMAGE"
	envAttacherImage            = "ATTACHER_IMAGE"
	envResizerImage             = "RESIZER_IMAGE"
	envSnapshotterImage         = "SNAPSHOTTER_IMAGE"
	envNodeDriverRegistrarImage = "NODE_DRIVER_REGISTRAR_IMAGE"
	envLivenessProbeImage       = "LIVENESS_PROBE_IMAGE"
)

var (
	sidecarReplacer = strings.NewReplacer(
		"${PROVISIONER_IMAGE}", os.Getenv(envProvisionerImage),
		"${ATTACHER_IMAGE}", os.Getenv(envAttacherImage),
		"${RESIZER_IMAGE}", os.Getenv(envResizerImage),
		"${SNAPSHOTTER_IMAGE}", os.Getenv(envSnapshotterImage),
		"${NODE_DRIVER_REGISTRAR_IMAGE}", os.Getenv(envNodeDriverRegistrarImage),
		"${LIVENESS_PROBE_IMAGE}", os.Getenv(envLivenessProbeImage),
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
