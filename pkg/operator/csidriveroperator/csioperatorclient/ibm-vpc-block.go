package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	IBMVPCBlockCSIDriverName          = "vpc.block.csi.ibm.io"
	envIBMVPCBlockDriverOperatorImage = "IBM_VPC_BLOCK_DRIVER_OPERATOR_IMAGE"
	envIBMVPCBlockDriverImage         = "IBM_VPC_BLOCK_DRIVER_IMAGE"
	envIBMVPCNodeLabelUpdaterImage    = "IBM_VPC_NODE_LABEL_UPDATER_IMAGE"
)

func isNotExternalTopologyMode(status *configv1.InfrastructureStatus) bool {
	if status == nil {
		return false
	}
	// IBM ROKS installations use ExternalTopologyMode and DO NOT need
	// CSO to deploy the VPC driver and operator like we do for IPI installs.
	if status.ControlPlaneTopology == configv1.ExternalTopologyMode {
		return false
	}
	return true
}

func GetIBMVPCBlockCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envIBMVPCBlockDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envIBMVPCBlockDriverImage),
		"${NODE_LABEL_IMAGE}", os.Getenv(envIBMVPCNodeLabelUpdaterImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   IBMVPCBlockCSIDriverName,
		ConditionPrefix: "IBMVPCBlock",
		Platform:        configv1.IBMCloudPlatformType,
		StatusFilter:    isNotExternalTopologyMode,
		StaticAssets: []string{
			"csidriveroperators/ibm-vpc-block/03_sa.yaml",
			"csidriveroperators/ibm-vpc-block/04_role.yaml",
			"csidriveroperators/ibm-vpc-block/05_rolebinding.yaml",
			"csidriveroperators/ibm-vpc-block/06_clusterrole.yaml",
			"csidriveroperators/ibm-vpc-block/07_clusterrolebinding.yaml",
		},
		CRAsset:         "csidriveroperators/ibm-vpc-block/09_cr.yaml",
		DeploymentAsset: "csidriveroperators/ibm-vpc-block/08_deployment.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}
}
