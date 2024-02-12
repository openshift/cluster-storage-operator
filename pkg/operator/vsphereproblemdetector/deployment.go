package vsphereproblemdetector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/management"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	appsinformersv1 "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"

	opv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

type VSphereProblemDetectorDeploymentController struct {
	name                    string
	manifest                []byte
	operatorClient          v1helpers.OperatorClientWithFinalizers
	kubeClient              kubernetes.Interface
	deployInformer          appsinformersv1.DeploymentInformer
	optionalManifestHooks   []deploymentcontroller.ManifestHookFunc
	optionalDeploymentHooks []deploymentcontroller.DeploymentHookFunc
}

func NewVSphereProblemDetectorDeploymentController(
	name string,
	manifest []byte,
	recorder events.Recorder,
	operatorClient v1helpers.OperatorClientWithFinalizers,
	kubeClient kubernetes.Interface,
	deployInformer appsinformersv1.DeploymentInformer,
	optionalInformers []factory.Informer,
	optionalManifestHooks []deploymentcontroller.ManifestHookFunc,
	optionalDeploymentHooks ...deploymentcontroller.DeploymentHookFunc,
) factory.Controller {
	c := &VSphereProblemDetectorDeploymentController{
		name:                    name,
		manifest:                manifest,
		operatorClient:          operatorClient,
		kubeClient:              kubeClient,
		deployInformer:          deployInformer,
		optionalManifestHooks:   optionalManifestHooks,
		optionalDeploymentHooks: optionalDeploymentHooks,
	}

	informers := append(
		optionalInformers,
		operatorClient.Informer(),
		deployInformer.Informer(),
	)

	return factory.New().WithInformers(
		informers...,
	).WithSync(
		c.sync,
	).ResyncEvery(
		time.Minute,
	).WithSyncDegradedOnError(
		operatorClient,
	).ToController(
		c.name,
		recorder.WithComponentSuffix(strings.ToLower(name)+"-deployment-controller-"),
	)
}

func (c *VSphereProblemDetectorDeploymentController) Name() string {
	return c.name
}

func (c *VSphereProblemDetectorDeploymentController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	opSpec, opStatus, _, err := c.operatorClient.GetOperatorState()
	if apierrors.IsNotFound(err) && management.IsOperatorRemovable() {
		return nil
	}
	if err != nil {
		return err
	}

	if opSpec.ManagementState != opv1.Managed {
		return nil
	}

	required, err := c.getDeployment(opSpec)
	if err != nil {
		return err
	}

	deployment, _, err := resourceapply.ApplyDeployment(
		ctx,
		c.kubeClient.AppsV1(),
		syncContext.Recorder(),
		required,
		resourcemerge.ExpectedDeploymentGeneration(required, opStatus.Generations),
	)
	if err != nil {
		return err
	}

	progressingCondition := opv1.OperatorCondition{
		Type:   c.name + opv1.OperatorStatusTypeProgressing,
		Status: opv1.ConditionFalse,
	}

	if ok, msg := isProgressing(deployment); ok {
		progressingCondition.Status = opv1.ConditionTrue
		progressingCondition.Message = msg
		progressingCondition.Reason = "Deploying"
	}

	updateStatusFn := func(newStatus *opv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&newStatus.Generations, deployment)
		return nil
	}

	_, _, err = v1helpers.UpdateStatus(
		ctx,
		c.operatorClient,
		updateStatusFn,
		v1helpers.UpdateConditionFn(progressingCondition),
	)

	return err
}

func (c *VSphereProblemDetectorDeploymentController) getDeployment(opSpec *opv1.OperatorSpec) (*appsv1.Deployment, error) {
	manifest := c.manifest
	for i := range c.optionalManifestHooks {
		var err error
		manifest, err = c.optionalManifestHooks[i](opSpec, manifest)
		if err != nil {
			return nil, fmt.Errorf("error running hook function (index=%d): %w", i, err)
		}
	}

	required := resourceread.ReadDeploymentV1OrDie(manifest)

	for i := range c.optionalDeploymentHooks {
		err := c.optionalDeploymentHooks[i](opSpec, required)
		if err != nil {
			return nil, fmt.Errorf("error running hook function (index=%d): %w", i, err)
		}
	}
	return required, nil
}

func isProgressing(deployment *appsv1.Deployment) (bool, string) {
	var deploymentExpectedReplicas int32
	if deployment.Spec.Replicas != nil {
		deploymentExpectedReplicas = *deployment.Spec.Replicas
	}

	switch {
	case deployment.Generation != deployment.Status.ObservedGeneration:
		return true, "Waiting for Deployment to act on changes"
	case deployment.Status.UnavailableReplicas > 0:
		return true, "Waiting for Deployment to deploy pods"
	case deployment.Status.UpdatedReplicas < deploymentExpectedReplicas:
		return true, "Waiting for Deployment to update pods"
	case deployment.Status.AvailableReplicas < deploymentExpectedReplicas:
		return true, "Waiting for Deployment to deploy pods"
	}
	return false, ""
}
