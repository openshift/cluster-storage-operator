package csoclients

import (
	"time"

	opv1 "github.com/openshift/api/operator/v1"
	fakeconfig "github.com/openshift/client-go/config/clientset/versioned/fake"
	cfginformers "github.com/openshift/client-go/config/informers/externalversions"
	fakeop "github.com/openshift/client-go/operator/clientset/versioned/fake"
	opinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	prominformer "github.com/prometheus-operator/prometheus-operator/pkg/client/informers/externalversions"
	fakemonitoring "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/fake"
	fakeextapi "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/dynamic/fake"
	fakecore "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/restmapper"
)

type FakeTestObjects struct {
	CoreObjects, ExtensionObjects, OperatorObjects, ConfigObjects, DynamicObjects, MonitoringObjects []runtime.Object
}

func WaitForSync(clients *Clients, stopCh <-chan struct{}) {
	clients.OperatorInformers.WaitForCacheSync(stopCh)
	clients.ExtensionInformer.WaitForCacheSync(stopCh)
	clients.KubeInformers.InformersFor("").WaitForCacheSync(stopCh)
	clients.ConfigInformers.WaitForCacheSync(stopCh)
}

type CrModifier func(cr *opv1.Storage) *opv1.Storage

func GetCR(modifiers ...CrModifier) *opv1.Storage {
	cr := &opv1.Storage{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: opv1.StorageSpec{
			OperatorSpec: opv1.OperatorSpec{
				ManagementState: opv1.Managed,
			},
		},
		Status: opv1.StorageStatus{},
	}
	for _, modifier := range modifiers {
		cr = modifier(cr)
	}
	return cr
}

func NewFakeClients(initialObjects *FakeTestObjects) *Clients {
	kubeClient := fakecore.NewSimpleClientset(initialObjects.CoreObjects...)
	kubeInformers := v1helpers.NewKubeInformersForNamespaces(kubeClient, informerNamespaces...)

	apiExtClient := fakeextapi.NewSimpleClientset(initialObjects.ExtensionObjects...)
	apiExtInformerFactory := apiextinformers.NewSharedInformerFactory(apiExtClient, 0 /*no resync */)

	operatorClient := fakeop.NewSimpleClientset(initialObjects.OperatorObjects...)
	operatorInformerFactory := opinformers.NewSharedInformerFactory(operatorClient, 0)

	configClient := fakeconfig.NewSimpleClientset(initialObjects.ConfigObjects...)
	configInformerFactory := cfginformers.NewSharedInformerFactory(configClient, 0)

	monitoringClient := fakemonitoring.NewSimpleClientset(initialObjects.MonitoringObjects...)
	monitoringInformer := prominformer.NewSharedInformerFactory(monitoringClient, 0)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)
	dynamicInformer := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)

	categoryExpander := restmapper.NewDiscoveryCategoryExpander(kubeClient.Discovery())
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(kubeClient.Discovery()))

	opInstance := makeFakeOperatorInstance(initialObjects)
	opClient := v1helpers.NewFakeOperatorClientWithObjectMeta(&opInstance.ObjectMeta, &opInstance.Spec.OperatorSpec, &opInstance.Status.OperatorStatus, nil /*triggerErr func*/)

	return &Clients{
		OperatorClient:          opClient,
		OperatorClientInformers: dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, time.Minute),
		KubeClient:              kubeClient,
		KubeInformers:           kubeInformers,
		ExtensionClientSet:      apiExtClient,
		ExtensionInformer:       apiExtInformerFactory,
		OperatorClientSet:       operatorClient,
		OperatorInformers:       operatorInformerFactory,
		ConfigClientSet:         configClient,
		ConfigInformers:         configInformerFactory,
		MonitoringClient:        monitoringClient,
		MonitoringInformer:      monitoringInformer,
		DynamicClient:           dynamicClient,
		DynamicInformer:         dynamicInformer,
		CategoryExpander:        categoryExpander,
		RestMapper:              restMapper,
	}
}

func makeFakeOperatorInstance(initialObjects *FakeTestObjects) *opv1.Storage {
	if len(initialObjects.OperatorObjects) > 0 {
		return initialObjects.OperatorObjects[0].(*opv1.Storage)
	}
	return &opv1.Storage{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cluster",
			Generation: 0,
		},
		Spec: opv1.StorageSpec{
			OperatorSpec: opv1.OperatorSpec{
				ManagementState: opv1.Managed,
			},
		},
		Status: opv1.StorageStatus{},
	}
}

func NewFakeMgmtClients(initialObjects *FakeTestObjects) *Clients {
	kubeClient := fakecore.NewSimpleClientset(initialObjects.CoreObjects...)
	kubeInformers := v1helpers.NewKubeInformersForNamespaces(kubeClient, informerNamespaces...)

	configClient := fakeconfig.NewSimpleClientset(initialObjects.ConfigObjects...)
	configInformerFactory := cfginformers.NewSharedInformerFactory(configClient, 0)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)
	dynamicInformer := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)

	return &Clients{
		KubeClient:      kubeClient,
		KubeInformers:   kubeInformers,
		ConfigClientSet: configClient,
		ConfigInformers: configInformerFactory,
		DynamicClient:   dynamicClient,
		DynamicInformer: dynamicInformer,
	}
}
