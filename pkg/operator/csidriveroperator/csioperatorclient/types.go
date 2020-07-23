package csioperatorclient

import (
	"strings"

	configv1 "github.com/openshift/api/config/v1"
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
	// StaticAssets is list of bindata assets to create when starting the CSI
	// driver operator.
	StaticAssets []string
	// CRAsset is name of the bindata asset with ClusterCSIDriver of the
	// operator. Its logLevel & operatorLoglevel will be set by CSO.
	CRAsset string
	// DeploymentAsset is name of the bindata asset with Deployment of the
	// operator. It will get updated by OCS in this way:
	// - ImageReplacer this CSIOperatorConfig is run.
	// - SidecarReplacer is run (see util.go)
	DeploymentAsset string
	// ImageReplacer is a replacer that's replaces CSI driver + operator image
	// names in the Deployment.
	ImageReplacer *strings.Replacer
	// Whether the CSI driver is optional (i.e. CSO is Available / not Degraded).
	Optional bool
}
