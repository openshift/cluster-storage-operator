package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	AWSEBSCSIDriverName          = "ebs.csi.aws.com"
	envAWSEBSDriverOperatorImage = "AWS_EBS_DRIVER_OPERATOR_IMAGE"
	envAWSEBSDriverImage         = "AWS_EBS_DRIVER_IMAGE"
)

func GetAWSEBSCSIOperatorConfig(isHypershift bool) CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envAWSEBSDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envAWSEBSDriverImage),
	}

	csiDriverConfig := CSIOperatorConfig{
		CSIDriverName:   AWSEBSCSIDriverName,
		ConditionPrefix: "AWSEBS",
		Platform:        configv1.AWSPlatformType,
		CRAsset:         "csidriveroperators/aws-ebs/10_cr.yaml",
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/aws-ebs/standalone/02_sa.yaml",
			"csidriveroperators/aws-ebs/standalone/03_role.yaml",
			"csidriveroperators/aws-ebs/standalone/04_rolebinding.yaml",
			"csidriveroperators/aws-ebs/standalone/05_clusterrole.yaml",
			"csidriveroperators/aws-ebs/standalone/06_clusterrolebinding.yaml",
			"csidriveroperators/aws-ebs/standalone/07_role_aws_config.yaml",
			"csidriveroperators/aws-ebs/standalone/08_rolebinding_aws_config.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/aws-ebs/standalone/09_deployment.yaml"
	} else {
		csiDriverConfig.GuestStaticAssets = []string{
			"csidriveroperators/aws-ebs/guest_assets/02_sa.yaml",
			"csidriveroperators/aws-ebs/guest_assets/03_role.yaml",
			"csidriveroperators/aws-ebs/guest_assets/04_rolebinding.yaml",
			"csidriveroperators/aws-ebs/guest_assets/05_clusterrole.yaml",
			"csidriveroperators/aws-ebs/guest_assets/06_clusterrolebinding.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/aws-ebs/mgmt_assets/01_operator_role.yaml",
			"csidriveroperators/aws-ebs/mgmt_assets/02_sa.yaml",
			"csidriveroperators/aws-ebs/mgmt_assets/04_rolebinding.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/aws-ebs/mgmt_assets/09_deployment.yaml"
	}

	return csiDriverConfig
}
