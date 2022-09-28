package operator

import (
	"context"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"time"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
)

const (
	resync = 20 * time.Minute
)

const (
	operatorNamespace      = "openshift-cluster-storage-operator"
	clusterOperatorName    = "storage"
	defaultScAnnotationKey = "storageclass.kubernetes.io/is-default-class"
)

func RunOperator(ctx context.Context, controllerConfig *controllercmd.ControllerContext, guestKubeConfig *string) error {
	isHyperShift := false
	if guestKubeConfig != nil {
		isHyperShift = true
	}

	if !isHyperShift {
		return startControllerStandAlone(ctx, controllerConfig)
	}
	return startHyperShiftController(ctx, controllerConfig, *guestKubeConfig)
}

func countStorageClasses(storageClassController factory.Controller, clients *csoclients.Clients) {
	klog.Infof("Registering default StorageClass count metric for controller %s", storageClassController.Name())
	legacyregistry.RawMustRegister(metrics.NewGaugeFunc(
		&metrics.GaugeOpts{
			Name:           "default_storage_class_count",
			Help:           "Number of default storage classes currently configured.",
			StabilityLevel: metrics.ALPHA,
		},
		func() float64 {
			scLister := clients.KubeInformers.InformersFor("").Storage().V1().StorageClasses().Lister()
			existingSCs, err := scLister.List(labels.Everything())
			if err != nil {
				klog.Fatalf("Failed to get existing storage classes: %s", err)
			}
			defaultSCCount := 0
			for _, sc := range existingSCs {
				if sc.Annotations[defaultScAnnotationKey] == "true" {
					defaultSCCount++
				}
			}
			klog.V(4).Infof("Current default StorageClass count %v", defaultSCCount)
			return float64(defaultSCCount)
		},
	))
}

func populateConfigs(clients *csoclients.Clients, recorder events.Recorder, isHypershift bool) []csioperatorclient.CSIOperatorConfig {
	if isHypershift {
		return []csioperatorclient.CSIOperatorConfig{
			csioperatorclient.GetAWSEBSCSIOperatorConfig(isHypershift),
		}
	}
	return []csioperatorclient.CSIOperatorConfig{
		csioperatorclient.GetAWSEBSCSIOperatorConfig(isHypershift),
		csioperatorclient.GetGCPPDCSIOperatorConfig(),
		csioperatorclient.GetOpenStackCinderCSIOperatorConfig(clients, recorder),
		csioperatorclient.GetOVirtCSIOperatorConfig(clients, recorder),
		csioperatorclient.GetManilaOperatorConfig(clients, recorder),
		csioperatorclient.GetVMwareVSphereCSIOperatorConfig(),
		csioperatorclient.GetAzureDiskCSIOperatorConfig(),
		csioperatorclient.GetAzureFileCSIOperatorConfig(),
		csioperatorclient.GetSharedResourceCSIOperatorConfig(),
		csioperatorclient.GetAlibabaDiskCSIOperatorConfig(),
		csioperatorclient.GetIBMVPCBlockCSIOperatorConfig(),
		csioperatorclient.GetPowerVSBlockCSIOperatorConfig(),
	}
}
