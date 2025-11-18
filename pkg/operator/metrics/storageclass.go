package metrics

import (
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
)

const (
	defaultScAnnotationKey = "storageclass.kubernetes.io/is-default-class"
)

func CountStorageClasses(clients *csoclients.Clients) {
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
