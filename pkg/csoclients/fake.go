package csoclients

import (
	fakeconfig "github.com/openshift/client-go/config/clientset/versioned/fake"
	cfginformers "github.com/openshift/client-go/config/informers/externalversions"
	fakeop "github.com/openshift/client-go/operator/clientset/versioned/fake"
	opinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	prominformer "github.com/prometheus-operator/prometheus-operator/pkg/client/informers/externalversions"
	fakemonitoring "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/fake"
	fakeextapi "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/runtime"
	fakecore "k8s.io/client-go/kubernetes/fake"
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

	//dynamicClient := fake.NewSimpleDynamicClient()

	opClient := operatorclient.OperatorClient{
		Client:    operatorClient,
		Informers: operatorInformerFactory,
	}

	return &Clients{
		OperatorClient:     &opClient,
		KubeClient:         kubeClient,
		KubeInformers:      kubeInformers,
		ExtensionClientSet: apiExtClient,
		ExtensionInformer:  apiExtInformerFactory,
		OperatorClientSet:  operatorClient,
		OperatorInformers:  operatorInformerFactory,
		ConfigClientSet:    configClient,
		ConfigInformers:    configInformerFactory,
		MonitoringClient:   monitoringClient,
		MonitoringInformer: monitoringInformer,
		//		DynamicClient:      dynamicClient,
	}
}
