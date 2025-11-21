package metrics

import (
	"errors"
	"testing"

	dto "github.com/prometheus/client_model/go"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/labels"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	"k8s.io/component-base/metrics"
)

func TestDriverParameterSets(t *testing.T) {
	vacs := []*storagev1.VolumeAttributesClass{
		{
			DriverName: "csi.foo",
			Parameters: map[string]string{
				"alpha": "1",
				"beta":  "2",
			},
		},
		{
			DriverName: "csi.foo",
			Parameters: map[string]string{
				"beta":  "2",
				"alpha": "1",
			},
		},
		{
			DriverName: "csi.bar",
			Parameters: map[string]string{
				"gamma": "3",
			},
		},
	}

	driverSets := buildDriverParameterSets(vacs)

	if len(driverSets) != 2 {
		t.Fatalf("expected 2 drivers, got %d", len(driverSets))
	}

	if len(driverSets["csi.foo"]) != 1 {
		t.Fatalf("expected driver csi.foo to have a single parameter key set, got %d", len(driverSets["csi.foo"]))
	}

	if driverSets["csi.foo"][buildParameterKeySet(map[string]string{"alpha": "1", "beta": "2"})] != 2 {
		t.Fatalf("expected driver csi.foo to have two VACs with identical parameter keys")
	}

	if len(driverSets["csi.bar"]) != 1 {
		t.Fatalf("expected driver csi.bar to have a single parameter key set, got %d", len(driverSets["csi.bar"]))
	}
}

func TestCalculateDriverMismatchValues(t *testing.T) {
	driverSets := map[string]map[keySet]int{
		"csi.foo": {
			"alpha,beta": 1,
			"beta,gamma": 1,
		},
		"csi.bar": {
			"alpha": 2,
		},
	}

	values := calculateDriverMismatchValues(driverSets)

	if values["csi.foo"] != 1.0 {
		t.Fatalf("expected csi.foo to have mismatch value 1.0, got %v", values["csi.foo"])
	}

	if values["csi.bar"] != 0.0 {
		t.Fatalf("expected csi.bar to have mismatch value 0.0, got %v", values["csi.bar"])
	}
}

func TestCollectWithStability(t *testing.T) {
	vacs := []*storagev1.VolumeAttributesClass{
		{
			DriverName: "csi.mismatch",
			Parameters: map[string]string{"alpha": "1"},
		},
		{
			DriverName: "csi.mismatch",
			Parameters: map[string]string{"beta": "2"},
		},
		{
			DriverName: "csi.match",
			Parameters: map[string]string{"alpha": "1"},
		},
		{
			DriverName: "csi.match",
			Parameters: map[string]string{"alpha": "1"},
		},
	}

	c := &collector{
		vacLister: &fakeVolumeAttributesClassLister{vacs: vacs},
	}

	if !c.BaseStableCollector.Create(nil, c) {
		t.Fatal("collector should have been created")
	}

	ch := make(chan metrics.Metric, 4)
	c.CollectWithStability(ch)

	if len(ch) != 2 {
		t.Fatalf("expected metrics for 2 drivers, got %d", len(ch))
	}

	results := make(map[string]float64)
	for i := 0; i < 2; i++ {
		metric := <-ch
		driver, value := extractMetric(metric, t)
		results[driver] = value
	}

	if results["csi.mismatch"] != 1.0 {
		t.Fatalf("expected csi.mismatch metric value 1.0, got %v", results["csi.mismatch"])
	}
	if results["csi.match"] != 0.0 {
		t.Fatalf("expected csi.match metric value 0.0, got %v", results["csi.match"])
	}
}

func extractMetric(metric metrics.Metric, t *testing.T) (driver string, value float64) {
	t.Helper()

	dtoMetric := &dto.Metric{}
	if err := metric.Write(dtoMetric); err != nil {
		t.Fatalf("failed to convert metric: %v", err)
	}

	if dtoMetric.GetGauge() == nil {
		t.Fatalf("expected gauge metric")
	}

	for _, label := range dtoMetric.GetLabel() {
		if label.GetName() == "driver" {
			driver = label.GetValue()
			break
		}
	}
	if driver == "" {
		t.Fatalf("driver label not found")
	}

	return driver, dtoMetric.GetGauge().GetValue()
}

type fakeVolumeAttributesClassLister struct {
	vacs []*storagev1.VolumeAttributesClass
	err  error
}

var _ storagelisters.VolumeAttributesClassLister = (*fakeVolumeAttributesClassLister)(nil)

func (f *fakeVolumeAttributesClassLister) List(selector labels.Selector) ([]*storagev1.VolumeAttributesClass, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vacs, nil
}

func (f *fakeVolumeAttributesClassLister) Get(name string) (*storagev1.VolumeAttributesClass, error) {
	return nil, errors.New("not implemented")
}
