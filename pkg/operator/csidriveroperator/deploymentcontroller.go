package csidriveroperator

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/assets"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/status"
)

const (
	deploymentControllerName = "CSIDriverOperatorDeployment"
)

func NewCSIDriverOperatorDeploymentController(
	clients *csoclients.Clients,
	csiOperatorConfig csioperatorclient.CSIOperatorConfig,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder,
	resyncInterval time.Duration,
) factory.Controller {

	deploymentBytes, err := assets.ReadFile(csiOperatorConfig.DeploymentAsset)
	if err != nil {
		panic(err)
	}

	manifestHooks, deploymentHooks := getCommonHooks(getCommonReplacers(csiOperatorConfig))
	deploymentHooks = append(deploymentHooks, getStandaloneNodeSelectorHook(clients.ConfigInformers.Config().V1().Infrastructures().Lister()))

	c, err := deploymentcontroller.NewDeploymentControllerBuilder(
		csiOperatorConfig.ConditionPrefix+deploymentControllerName,
		deploymentBytes,
		eventRecorder,
		clients.OperatorClient,
		clients.KubeClient,
		clients.KubeInformers.InformersFor(csoclients.CSIOperatorNamespace).Apps().V1().Deployments(),
	).WithConditions(
		// Explicitly disable Available condition to avoid prematurely cascading
		// up to the clusteroperator CR a potential Available=false.
		operatorv1.OperatorStatusTypeProgressing,
		operatorv1.OperatorStatusTypeDegraded,
	).WithExtraInformers(
		clients.ConfigInformers.Config().V1().Infrastructures().Informer(),
	).WithManifestHooks(
		manifestHooks...,
	).WithDeploymentHooks(
		deploymentHooks...,
	).WithPostStartHooks(
		initalSync,
	).ToController()
	if err != nil {
		panic(err)
	}
	return c
}

func getCommonReplacers(csiOperatorConfig csioperatorclient.CSIOperatorConfig) []*strings.Replacer {
	replacers := []*strings.Replacer{sidecarReplacer}
	if csiOperatorConfig.ImageReplacer != nil {
		replacers = append(replacers, csiOperatorConfig.ImageReplacer)
	}
	return replacers
}

func getCommonHooks(replacers []*strings.Replacer) ([]deploymentcontroller.ManifestHookFunc, []deploymentcontroller.DeploymentHookFunc) {
	return []deploymentcontroller.ManifestHookFunc{
			getReplacerHook(replacers),
			getLogLevelHook(),
		}, []deploymentcontroller.DeploymentHookFunc{
			getProxyHook(),
		}
}

func getReplacerHook(replacers []*strings.Replacer) deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorv1.OperatorSpec, deploymentBytes []byte) ([]byte, error) {
		deploymentString := string(deploymentBytes)

		// Replace images
		for _, replacer := range replacers {
			// Replace images
			if replacer != nil {
				deploymentString = replacer.Replace(deploymentString)
			}
		}

		return []byte(deploymentString), nil
	}
}

func getLogLevelHook() deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorv1.OperatorSpec, deploymentBytes []byte) ([]byte, error) {
		logLevel := loglevel.LogLevelToVerbosity(spec.LogLevel)
		deploymentBytes = bytes.ReplaceAll(deploymentBytes, []byte("${LOG_LEVEL}"), []byte(strconv.Itoa(logLevel)))
		return deploymentBytes, nil
	}
}

func getProxyHook() deploymentcontroller.DeploymentHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		return util.InjectObservedProxyInDeploymentContainers(deployment, spec)
	}
}

func getStandaloneNodeSelectorHook(infraLister configv1listers.InfrastructureLister) deploymentcontroller.DeploymentHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment *appsv1.Deployment) error {
		infra, err := infraLister.Get(infraConfigName)
		if err != nil {
			return fmt.Errorf("failed to get infrastructure resource: %w", err)
		}
		if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
			deployment.Spec.Template.Spec.NodeSelector = map[string]string{}
		}
		return nil
	}
}
