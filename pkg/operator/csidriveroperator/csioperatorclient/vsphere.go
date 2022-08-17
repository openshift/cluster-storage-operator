package csioperatorclient

import (
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	VMwareVSphereDriverName             = "csi.vsphere.vmware.com"
	envVMwareVSphereDriverOperatorImage = "VMWARE_VSPHERE_DRIVER_OPERATOR_IMAGE"
	envVMwareVSphereDriverImage         = "VMWARE_VSPHERE_DRIVER_IMAGE"
	envVMWareVsphereDriverSyncerImage   = "VMWARE_VSPHERE_SYNCER_IMAGE"
)

func GetVMwareVSphereCSIOperatorConfig() CSIOperatorConfig {
	pairs := []string{
		"${OPERATOR_IMAGE}", os.Getenv(envVMwareVSphereDriverOperatorImage),
		"${DRIVER_IMAGE}", os.Getenv(envVMwareVSphereDriverImage),
		"${VMWARE_VSPHERE_SYNCER_IMAGE}", os.Getenv(envVMWareVsphereDriverSyncerImage),
	}

	return CSIOperatorConfig{
		CSIDriverName:   VMwareVSphereDriverName,
		ConditionPrefix: "VSphere",
		Platform:        configv1.VSpherePlatformType,
		StaticAssets: []string{
			"csidriveroperators/vsphere/02_configmap.yaml",
			"csidriveroperators/vsphere/03_sa.yaml",
			"csidriveroperators/vsphere/04_role.yaml",
			"csidriveroperators/vsphere/05_rolebinding.yaml",
			"csidriveroperators/vsphere/06_clusterrole.yaml",
			"csidriveroperators/vsphere/07_clusterrolebinding.yaml",
			"csidriveroperators/vsphere/11_service.yaml",
			"csidriveroperators/vsphere/13_prometheus_role.yaml",
			"csidriveroperators/vsphere/14_prometheus_rolebinding.yaml",
			"csidriveroperators/vsphere/15_prometheusrules.yaml",
		},
		ServiceMonitorAsset: "csidriveroperators/vsphere/12_servicemonitor.yaml",
		CRAsset:             "csidriveroperators/vsphere/09_cr.yaml",
		DeploymentAsset:     "csidriveroperators/vsphere/08_deployment.yaml",
		ImageReplacer:       strings.NewReplacer(pairs...),
		AllowDisabled:       false,
	}
}
