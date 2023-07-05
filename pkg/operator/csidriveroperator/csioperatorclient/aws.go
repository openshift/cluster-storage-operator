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
		ImageReplacer:   strings.NewReplacer(pairs...),
		AllowDisabled:   false,
	}

	if !isHypershift {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/aws-ebs/standalone/generated/v1_serviceaccount_aws-ebs-csi-driver-operator.yaml",
			"csidriveroperators/aws-ebs/standalone/generated/rbac.authorization.k8s.io_v1_role_aws-ebs-csi-driver-operator-role.yaml",
			"csidriveroperators/aws-ebs/standalone/generated/rbac.authorization.k8s.io_v1_rolebinding_aws-ebs-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/aws-ebs/standalone/generated/rbac.authorization.k8s.io_v1_clusterrole_aws-ebs-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/aws-ebs/standalone/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_aws-ebs-csi-driver-operator-clusterrolebinding.yaml",
			"csidriveroperators/aws-ebs/standalone/07_role_aws_config.yaml",
			"csidriveroperators/aws-ebs/standalone/08_rolebinding_aws_config.yaml",
		}
		csiDriverConfig.CRAsset = "csidriveroperators/aws-ebs/standalone/generated/operator.openshift.io_v1_clustercsidriver_ebs.csi.aws.com.yaml"
		csiDriverConfig.DeploymentAsset = "csidriveroperators/aws-ebs/standalone/generated/apps_v1_deployment_aws-ebs-csi-driver-operator.yaml"
	} else {
		csiDriverConfig.StaticAssets = []string{
			"csidriveroperators/aws-ebs/hypershift/guest/generated/v1_serviceaccount_aws-ebs-csi-driver-operator.yaml",
			"csidriveroperators/aws-ebs/hypershift/guest/generated/rbac.authorization.k8s.io_v1_role_aws-ebs-csi-driver-operator-role.yaml",
			"csidriveroperators/aws-ebs/hypershift/guest/generated/rbac.authorization.k8s.io_v1_rolebinding_aws-ebs-csi-driver-operator-rolebinding.yaml",
			"csidriveroperators/aws-ebs/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrole_aws-ebs-csi-driver-operator-clusterrole.yaml",
			"csidriveroperators/aws-ebs/hypershift/guest/generated/rbac.authorization.k8s.io_v1_clusterrolebinding_aws-ebs-csi-driver-operator-clusterrolebinding.yaml",
		}
		csiDriverConfig.MgmtStaticAssets = []string{
			"csidriveroperators/aws-ebs/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_role_aws-ebs-csi-driver-operator-role.yaml",
			"csidriveroperators/aws-ebs/hypershift/mgmt/generated/v1_serviceaccount_aws-ebs-csi-driver-operator.yaml",
			"csidriveroperators/aws-ebs/hypershift/mgmt/generated/rbac.authorization.k8s.io_v1_rolebinding_aws-ebs-csi-driver-operator-rolebinding.yaml",
		}
		csiDriverConfig.DeploymentAsset = "csidriveroperators/aws-ebs/hypershift/mgmt/generated/apps_v1_deployment_aws-ebs-csi-driver-operator.yaml"
		csiDriverConfig.CRAsset = "csidriveroperators/aws-ebs/hypershift/guest/generated/operator.openshift.io_v1_clustercsidriver_ebs.csi.aws.com.yaml"
	}

	return csiDriverConfig
}
