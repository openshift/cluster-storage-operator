package util

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

// InjectObservedProxyInDeploymentContainers takes an observed proxy config and adds it to the containers in the Deployment.
func InjectObservedProxyInDeploymentContainers(deployment *appsv1.Deployment, opSpec *operatorapi.OperatorSpec) error {
	containerNamesString := deployment.Annotations["config.openshift.io/inject-proxy"]
	err := v1helpers.InjectObservedProxyIntoContainers(
		&deployment.Spec.Template.Spec,
		strings.Split(containerNamesString, ","),
		opSpec.ObservedConfig.Raw,
		ProxyConfigPath()...,
	)
	if err != nil {
		return err
	}
	return nil
}

// ProxyConfigPath returns the path for the observed proxy config. This is a
// function to avoid exposing a slice that could potentially be appended.
func ProxyConfigPath() []string {
	return []string{"targetconfig", "proxy"}
}
