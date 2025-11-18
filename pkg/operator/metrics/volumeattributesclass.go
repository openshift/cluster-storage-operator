package metrics

import (
	"sort"
	"strings"
	"sync"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/labels"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
)

type collector struct {
	metrics.BaseStableCollector
	vacLister storagelisters.VolumeAttributesClassLister
}

var (
	vacParameterMismatchDesc = metrics.NewDesc(
		"openshift_cluster_storage_vac_mismatch_parameters",
		"Indicates whether VolumeAttributeClasses (VAC) for a given driver have mismatching parameters. 0 means all VACs are ok, 1 means some VACs have different parameter names than the others.",
		[]string{"driver"},
		nil,
		metrics.ALPHA,
		"",
	)
)

var registerMetrics sync.Once

func InitializeVACMismatchMetrics(clients *csoclients.Clients) {
	klog.Infof("Registering VolumeAttributesClass parameter mismatch metric collector")
	registerMetrics.Do(func() {
		legacyregistry.CustomMustRegister(newCollector(clients))
	})
}

func newCollector(clients *csoclients.Clients) *collector {
	return &collector{
		vacLister: clients.KubeInformers.InformersFor("").Storage().V1().VolumeAttributesClasses().Lister(),
	}
}

var _ metrics.StableCollector = &collector{}

func (c *collector) DescribeWithStability(ch chan<- *metrics.Desc) {
	ch <- vacParameterMismatchDesc
}

func (c *collector) CollectWithStability(ch chan<- metrics.Metric) {
	vacs, err := c.vacLister.List(labels.Everything())
	if err != nil {
		return
	}

	driverSets := buildDriverParameterSets(vacs)
	driverValues := calculateDriverMismatchValues(driverSets)

	for driver, value := range driverValues {
		if value == 1.0 {
			klog.V(2).Infof("VolumeAttributeClasses (VAC) for driver %s have mismatching parameters. Please make sure that all VACs of the CSI driver %s have the same parameter names.", driver, driver)
		}
		ch <- metrics.NewLazyConstMetric(vacParameterMismatchDesc, metrics.GaugeValue, value, driver)
		klog.V(4).Infof("VAC parameter mismatch check for driver %s: %v", driver, value)
	}
}

type keySet string

func buildDriverParameterSets(vacs []*storagev1.VolumeAttributesClass) map[string]map[keySet]int {
	driverSets := map[string]map[keySet]int{}

	for _, vac := range vacs {
		key := buildParameterKeySet(vac.Parameters)
		driver := vac.DriverName
		if _, ok := driverSets[driver]; !ok {
			driverSets[driver] = map[keySet]int{}
		}
		driverSets[driver][key]++
	}

	return driverSets
}

func buildParameterKeySet(parameters map[string]string) keySet {
	keys := make([]string, 0, len(parameters))
	for k := range parameters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keySet(strings.Join(keys, ","))
}

func calculateDriverMismatchValues(driverSets map[string]map[keySet]int) map[string]float64 {
	values := make(map[string]float64, len(driverSets))
	for driver, sets := range driverSets {
		value := 0.0
		if len(sets) > 1 {
			value = 1.0
		}
		values[driver] = value
	}
	return values
}
