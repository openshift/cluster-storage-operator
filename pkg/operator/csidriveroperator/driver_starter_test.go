package csidriveroperator

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	cfgv1 "github.com/openshift/api/config/v1"
	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/csidriveroperator/csioperatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/errors"
)

func TestShouldRunController(t *testing.T) {
	testingDefault := featuregates.NewFeatureGate(nil, []v1.FeatureGateName{v1.FeatureGateCSIDriverSharedResource})
	testingTechPreview := featuregates.NewFeatureGate([]v1.FeatureGateName{v1.FeatureGateCSIDriverSharedResource}, nil)
	customFeatureGate := featuregates.NewFeatureGate([]v1.FeatureGateName{"SomeOtherFeatureGate", v1.FeatureGateCSIDriverSharedResource, "YetAnotherGate"}, nil)
	customWithJustOther := featuregates.NewFeatureGate([]v1.FeatureGateName{"SomeOtherFeatureGate"}, nil)
	customWithNothing := featuregates.NewFeatureGate([]v1.FeatureGateName{}, nil)

	tests := []struct {
		name        string
		platform    v1.PlatformType
		featureGate featuregates.FeatureGate
		csiDriver   *storagev1.CSIDriver
		config      csioperatorclient.CSIOperatorConfig
		expectRun   bool
		expectError bool
	}{
		{
			"tech preview Shared Resource driver on AllPlatforms type",
			v1.AWSPlatformType,
			testingTechPreview,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           csioperatorclient.AllPlatforms,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver on AWSPlatformType",
			v1.AWSPlatformType,
			testingTechPreview,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver on GCPPlatformType",
			v1.GCPPlatformType,
			testingTechPreview,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.GCPPlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver on GCPPlatformType",
			v1.VSpherePlatformType,
			testingTechPreview,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.VSpherePlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			true,
			false,
		},
		{
			"GA CSI driver on matching platform",
			v1.AWSPlatformType,
			testingDefault,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "ebs.csi.aws.com",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "",
			},
			true,
			false,
		},
		{
			"GA CSI driver on non-matching platform",
			v1.GCPPlatformType,
			testingDefault,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "ebs.csi.aws.com",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: "",
			},
			false,
			false,
		},
		{
			"GA CSI driver with StatusFilter returning true",
			v1.IBMCloudPlatformType,
			testingDefault,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vpc.block.csi.ibm.io",
				Platform:           v1.IBMCloudPlatformType,
				RequireFeatureGate: "",
				StatusFilter: func(*v1.InfrastructureStatus) bool {
					return true
				},
			},
			true,
			false,
		},
		{
			"GA CSI driver with StatusFilter returning false",
			v1.IBMCloudPlatformType,
			testingDefault,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "vpc.block.csi.ibm.io",
				Platform:           v1.IBMCloudPlatformType,
				RequireFeatureGate: "",
				StatusFilter: func(*v1.InfrastructureStatus) bool {
					return false
				},
			},
			false,
			false,
		},
		{
			"tech preview Shared Resource driver with positive custom featureGate",
			v1.AWSPlatformType,
			customFeatureGate,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			true,
			false,
		},
		{
			"tech preview Shared Resource driver with negative custom featureGate",
			v1.AWSPlatformType,
			customWithJustOther,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			false,
			false,
		},
		{
			"tech preview Shared Resource driver with empty custom featureGate",
			v1.AWSPlatformType,
			customWithNothing,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			false,
			false,
		},
		{
			"tech preview Shared Resource driver with nil custom featureGate",
			v1.AWSPlatformType,
			customWithNothing,
			nil,
			csioperatorclient.CSIOperatorConfig{
				CSIDriverName:      "csi.sharedresource.openshift.io",
				Platform:           v1.AWSPlatformType,
				RequireFeatureGate: v1.FeatureGateCSIDriverSharedResource,
			},
			false,
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			infra := &v1.Infrastructure{
				Status: v1.InfrastructureStatus{
					PlatformStatus: &v1.PlatformStatus{
						Type: test.platform,
					},
				},
			}
			res, err := shouldRunController(test.config, infra, test.featureGate, test.csiDriver)
			if res != test.expectRun {
				t.Errorf("Expected run %t, got %t", test.expectRun, res)
			}
			gotError := err != nil
			if gotError != test.expectError {
				t.Errorf("Expected error %t, got %t: %s", test.expectError, res, err)
			}
		})
	}
}

func csiDriver(csiDriverName string, annotations map[string]string) *storagev1.CSIDriver {
	return &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name:        csiDriverName,
			Annotations: annotations,
		},
		Spec: storagev1.CSIDriverSpec{},
	}
}

func TestIsNoMatchError(t *testing.T) {
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "foos",
	}
	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Foo",
	}
	gk := schema.GroupKind{
		Group: "",
		Kind:  "foo",
	}

	tests := []struct {
		name          string
		err           error
		expectNoMatch bool
	}{
		{
			name:          "no error",
			err:           nil,
			expectNoMatch: false,
		},
		{
			name:          "NoResourceMatch",
			err:           &meta.NoResourceMatchError{PartialResource: gvr},
			expectNoMatch: true,
		},
		{
			name:          "NoKindMatch",
			err:           &meta.NoKindMatchError{GroupKind: gk},
			expectNoMatch: true,
		},
		{
			name: "unrelated error",
			err: &meta.AmbiguousKindError{
				PartialKind:       gvk,
				MatchingResources: nil,
				MatchingKinds:     nil,
			},
			expectNoMatch: false,
		},
		{
			name:          "aggregated NoResourceMatch",
			err:           errors.NewAggregate([]error{&meta.NoResourceMatchError{PartialResource: gvr}, os.ErrPermission}),
			expectNoMatch: true,
		},
		{
			name:          "aggregated NoKindMatch",
			err:           errors.NewAggregate([]error{os.ErrPermission, &meta.NoKindMatchError{GroupKind: gk}}),
			expectNoMatch: true,
		},
		{
			name:          "aggregated unrelated errors",
			err:           errors.NewAggregate([]error{os.ErrPermission, os.ErrExist, fs.ErrClosed}),
			expectNoMatch: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ret := isNoMatchError(test.err)
			if ret != test.expectNoMatch {
				t.Errorf("expected %t, got %t", test.expectNoMatch, ret)
			}
		})
	}
}

func getInfrastructure(platformType cfgv1.PlatformType) *cfgv1.Infrastructure {
	return &cfgv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: infraConfigName,
		},
		Status: cfgv1.InfrastructureStatus{
			PlatformStatus: &cfgv1.PlatformStatus{
				Type: platformType,
			},
		},
	}
}

func getDefaultFeatureGate() *cfgv1.FeatureGate {
	return &cfgv1.FeatureGate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: cfgv1.FeatureGateSpec{},
	}
}

func TestStandAloneStarter(t *testing.T) {
	initialObjects := &csoclients.FakeTestObjects{}
	initialObjects.OperatorObjects = append(initialObjects.OperatorObjects, csoclients.GetCR())
	initialObjects.ConfigObjects = append(initialObjects.ConfigObjects, getInfrastructure(cfgv1.AWSPlatformType))
	initialObjects.ConfigObjects = append(initialObjects.ConfigObjects, getDefaultFeatureGate())

	finish, cancel := context.WithCancel(context.TODO())
	defer cancel()

	clients := csoclients.NewFakeClients(initialObjects)

	storageInformer := clients.OperatorInformers.Operator().V1().Storages().Informer()
	storageInformer.GetStore().Add(csoclients.GetCR())

	infrInformer := clients.ConfigInformers.Config().V1().Infrastructures().Informer()
	infrInformer.GetStore().Add(getInfrastructure(cfgv1.AWSPlatformType))
	testingDefault := featuregates.NewFeatureGate(nil, []v1.FeatureGateName{v1.FeatureGateCSIDriverSharedResource})

	csoclients.StartInformers(clients, finish.Done())
	csoclients.WaitForSync(clients, finish.Done())

	awsConfig := []csioperatorclient.CSIOperatorConfig{csioperatorclient.GetAWSEBSCSIOperatorConfig(false)}

	standAloneStarter := NewStandaloneDriverStarter(clients,
		testingDefault,
		20*time.Minute,
		status.NewVersionGetter(),
		"",
		events.NewInMemoryRecorder(csiDriverControllerName),
		awsConfig)
	t.Logf("standAloneDriverStarter is: %+v", standAloneStarter)
	err := standAloneStarter.Sync(context.TODO(), factory.NewSyncContext("foobar", events.NewInMemoryRecorder(csiDriverControllerName)))

	if err != nil {
		t.Logf("got error running sync: %+v", err)
	}
}
