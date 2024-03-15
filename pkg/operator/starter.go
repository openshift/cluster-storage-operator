package operator

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
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
	if guestKubeConfig != nil && *guestKubeConfig != "" {
		isHyperShift = true
	}

	starter := NewStandaloneStarter(controllerConfig)

	if isHyperShift {
		starter = NewHyperShiftStarter(controllerConfig, *guestKubeConfig)
	}
	return starter.StartOperator(ctx)
}

func countStorageClasses(clients *csoclients.Clients) {
	klog.Infof("Registering default StorageClass count metric for controller")
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
			var defaultSCNames []string
			for _, sc := range existingSCs {
				if sc.Annotations[defaultScAnnotationKey] == "true" {
					defaultSCCount++
					defaultSCNames = append(defaultSCNames, sc.Name)
				}
			}
			klog.V(4).Infof("Current default StorageClass count: %v (%v)", defaultSCCount, defaultSCNames)
			return float64(defaultSCCount)
		},
	))
}
