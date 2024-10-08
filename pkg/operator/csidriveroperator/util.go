package csidriveroperator

import (
	"context"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"

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
	envKubeRBACProxyControlPlaneImage = "CONTROL_PLANE_KUBE_RBAC_PROXY_IMAGE"
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

func reportUpdateEvent(recorder events.Recorder, obj runtime.Object, originalErr error, details ...string) {
	gvk := resourcehelper.GuessObjectGroupVersionKind(obj)
	switch {
	case originalErr != nil:
		recorder.Warningf(fmt.Sprintf("%sUpdateFailed", gvk.Kind), "Failed to update %s: %v", resourcehelper.FormatResourceForCLIWithNamespace(obj), originalErr)
	case len(details) == 0:
		recorder.Eventf(fmt.Sprintf("%sUpdated", gvk.Kind), "Updated %s because it changed", resourcehelper.FormatResourceForCLIWithNamespace(obj))
	default:
		recorder.Eventf(fmt.Sprintf("%sUpdated", gvk.Kind), "Updated %s:\n%s", resourcehelper.FormatResourceForCLIWithNamespace(obj), strings.Join(details, "\n"))
	}
}

func checkDeploymentHealth(ctx context.Context, c appsclientv1.DeploymentsGetter, d *appsv1.Deployment) error {
	d, err := c.Deployments(d.Namespace).Get(ctx, d.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	name := fmt.Sprintf("%s/%s", d.Namespace, d.Name)
	progressing := getDeploymentCondition(appsv1.DeploymentProgressing, &d.Status)
	if progressing != nil && progressing.Status == corev1.ConditionFalse && progressing.Reason == "ProgressDeadlineExceeded" {
		return fmt.Errorf("deployment %s is %s=%s: %s: %s", name, progressing.Type, progressing.Status, progressing.Reason, progressing.Message)
	}

	replicaFailure := getDeploymentCondition(appsv1.DeploymentReplicaFailure, &d.Status)
	if replicaFailure != nil && replicaFailure.Status == corev1.ConditionTrue {
		return fmt.Errorf("deployment %s has some pods failing; unavailable replicas=%d", name, d.Status.UnavailableReplicas)
	}

	available := getDeploymentCondition(appsv1.DeploymentAvailable, &d.Status)
	if available != nil && available.Status == corev1.ConditionFalse && progressing != nil && progressing.Status == corev1.ConditionFalse {
		return fmt.Errorf("deployment %s is not available and not progressing; updated replicas=%d of %d, available replicas=%d of %d",
			name,
			d.Status.UpdatedReplicas,
			d.Status.Replicas,
			d.Status.AvailableReplicas,
			d.Status.Replicas)
	}

	return nil
}

func getDeploymentCondition(condType appsv1.DeploymentConditionType, status *appsv1.DeploymentStatus) *appsv1.DeploymentCondition {
	for i := range status.Conditions {
		if status.Conditions[i].Type == condType {
			return &status.Conditions[i]
		}
	}
	return nil
}
