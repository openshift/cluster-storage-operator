package utils

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	operatorapi "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

type DeploymentOptions struct {
	Required       *appsv1.Deployment
	ControllerName string
	OpStatus       *operatorapi.OperatorStatus
	EventRecorder  events.Recorder
	KubeClient     kubernetes.Interface
	OperatorClient v1helpers.OperatorClient
	TargetVersion  string
	VersionGetter  status.VersionGetter
	VersionName    string
}

func CreateDeployment(ctx context.Context, depOpts DeploymentOptions) (*appsv1.Deployment, error) {
	lastGeneration := resourcemerge.ExpectedDeploymentGeneration(depOpts.Required, depOpts.OpStatus.Generations)
	deployment, _, err := resourceapply.ApplyDeployment(ctx, depOpts.KubeClient.AppsV1(), depOpts.EventRecorder, depOpts.Required, lastGeneration)
	if err != nil {
		// This will set Degraded condition
		return nil, err
	}

	// Available: at least one replica is running
	// deploymentAvailable := operatorapi.OperatorCondition{
	// Type: depOpts.ControllerName + operatorapi.OperatorStatusTypeAvailable,
	// }
	deploymentAvailable := applyoperatorv1.OperatorCondition().
		WithType(depOpts.ControllerName + operatorapi.OperatorStatusTypeAvailable)

	if deployment.Status.AvailableReplicas > 0 {
		deploymentAvailable = deploymentAvailable.
			WithStatus(operatorapi.ConditionTrue)
	} else {
		deploymentAvailable = deploymentAvailable.
			WithStatus(operatorapi.ConditionFalse).
			WithReason("Deploying").
			WithMessage("Waiting for a Deployment pod to start")
	}

	// Not progressing: all replicas are at the latest version && Deployment generation matches
	deploymentProgressing := applyoperatorv1.OperatorCondition().
		WithType(depOpts.ControllerName + operatorapi.OperatorStatusTypeProgressing)

	if deployment.Status.ObservedGeneration != deployment.Generation {
		msg := fmt.Sprintf("desired generation %d, current generation %d", deployment.Generation, deployment.Status.ObservedGeneration)
		deploymentProgressing = deploymentProgressing.
			WithStatus(operatorapi.ConditionTrue).
			WithReason("NewGeneration").
			WithMessage(msg)
	} else {
		if deployment.Spec.Replicas != nil {
			if deployment.Status.UpdatedReplicas == *deployment.Spec.Replicas {
				deploymentProgressing = deploymentProgressing.WithStatus(operatorapi.ConditionFalse)
				// All replicas were updated, set the version
				depOpts.VersionGetter.SetVersion(depOpts.VersionName, depOpts.TargetVersion)
			} else {
				msg := fmt.Sprintf("%d out of %d pods running", deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas)
				deploymentProgressing = deploymentProgressing.
					WithStatus(operatorapi.ConditionTrue).
					WithReason("Deploying").
					WithMessage(msg)
			}
		}
	}

	// Create a partial status with conditions and generations
	status := applyoperatorv1.OperatorStatus().
		WithConditions(deploymentAvailable, deploymentProgressing).
		WithGenerations(&applyoperatorv1.GenerationStatusApplyConfiguration{
			Group:          ptr.To("apps"),
			Resource:       ptr.To("deployments"),
			Namespace:      ptr.To(deployment.Namespace),
			Name:           ptr.To(deployment.Name),
			LastGeneration: ptr.To(deployment.Generation),
		})
	err = depOpts.OperatorClient.ApplyOperatorStatus(
		ctx,
		factory.ControllerFieldManager("CreateDeployment", "updateOperatorStatus"),
		status,
	)
	if err != nil {
		return nil, err
	}

	return deployment, nil
}

// GetRequiredDeployment returns a deployment from given assset after replacing necessary strings and setting
// correct log level.
func GetRequiredDeployment(deploymentAsset string, spec *operatorapi.OperatorSpec, nodeSelector map[string]string, tolerations []corev1.Toleration, replacers ...*strings.Replacer) (*appsv1.Deployment, error) {
	deploymentBytes, err := assets.ReadFile(deploymentAsset)
	if err != nil {
		return nil, err
	}
	deploymentString := string(deploymentBytes)

	for _, replacer := range replacers {
		// Replace images
		if replacer != nil {
			deploymentString = replacer.Replace(deploymentString)
		}
	}

	// Replace log level
	logLevel := loglevel.LogLevelToVerbosity(spec.LogLevel)
	deploymentString = strings.ReplaceAll(deploymentString, "${LOG_LEVEL}", strconv.Itoa(logLevel))

	deployment := resourceread.ReadDeploymentV1OrDie([]byte(deploymentString))
	if nodeSelector != nil {
		deployment.Spec.Template.Spec.NodeSelector = nodeSelector
	}

	deployment.Spec.Template.Spec.Tolerations = append(deployment.Spec.Template.Spec.Tolerations, tolerations...)

	return deployment, nil
}
