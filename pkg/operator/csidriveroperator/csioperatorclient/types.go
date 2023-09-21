package csioperatorclient

import (
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// AllPlatforms is a special PlatformType that indicates a CSI driver is installable on any cloud provider.
	// It is only meant to be used by the CSIOperatorConfig, and does not represent a real OpenShift platform type.
	AllPlatforms configv1.PlatformType = "AllPlatforms"
)

// CSIOperatorConfig is configuration of a CSI driver operator.
type CSIOperatorConfig struct {
	// Name of the CSI driver (such as ebs.csi.aws.com) and at the same time
	// name of ClusterCSIDriver CR.
	CSIDriverName string
	// Short name of the driver, used to prefix conditions.
	ConditionPrefix string
	// Platform where the driver should run.
	Platform configv1.PlatformType
	// StatusFilter is an optional callback to determine whether the operator
	// should run based on the InfrastructureStatus. Return true to proceed
	// like normal and run the usual checks (Platform, RequireFeatureGate, etc).
	// Return false if the operator should NOT run based on InfrastructureStatus.
	// If StatusFilter is nil, it is assumed to be true and will proceed like
	// normal and run the usual checks.
	StatusFilter func(*configv1.InfrastructureStatus, bool) bool
	// StaticAssets is list of bindata assets to create when starting the CSI
	// driver operator in standalone OCP clusters
	StaticAssets []string

	// MgmtStaticAssets returns a list of bindata assets to create in mgmt cluster
	// when starting the driver in hypershift clusters
	MgmtStaticAssets []string

	// CRAsset is name of the bindata asset with ClusterCSIDriver of the
	// operator. Its logLevel & operatorLoglevel will be set by CSO.
	CRAsset string
	// ServiceMonitorAsset is the name of the bindata asset with the ServiceMonitor
	ServiceMonitorAsset string
	// DeploymentAsset is name of the bindata asset with Deployment of the
	// operator. It will get updated by OCS in this way:
	// - ImageReplacer this CSIOperatorConfig is run.
	// - SidecarReplacer is run (see util.go)
	DeploymentAsset string
	// ImageReplacer is a replacer that's replaces CSI driver + operator image
	// names in the Deployment.
	ImageReplacer *strings.Replacer
	// Whether the CSI driver can set Disabled condition (i.e. the cloud may not support it) and it's OK.
	// In this case, the CSO's overall Available / Progressing conditions will not be affected by Disabled
	// ClusterCSIDriver.
	AllowDisabled bool
	// Extra controllers to start with the CSI driver operator
	ExtraControllers []factory.Controller
	// OLMOptions configuration of migration from OLM to CSO
	OLMOptions *OLMOptions
	// Run the CSI driver operator only when given FeatureGate is enabled
	RequireFeatureGate configv1.FeatureGateName
}

// OLMOptions contains information that is necessary to remove old CSI driver
// operator from OLM.
type OLMOptions struct {
	// Name of Deployment of OLM-managed operator. The namespace is autodetected from Subscription.
	OLMOperatorDeploymentName string
	// Name of package in OLM
	OLMPackageName string
	// Resource of the old operator CR
	CRResource schema.GroupVersionResource
}
