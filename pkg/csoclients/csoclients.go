package csoclients

import (
	"time"

	cfgclientset "github.com/openshift/client-go/config/clientset/versioned"
	cfginformers "github.com/openshift/client-go/config/informers/externalversions"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	opinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	prominformer "github.com/prometheus-operator/prometheus-operator/pkg/client/informers/externalversions"
	promclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Clients struct {
	// Client for CSO's CR
	OperatorClient *operatorclient.OperatorClient
	// Kubernetes API client
	KubeClient kubernetes.Interface
	// Kubernetes API informers, per namespace
	KubeInformers v1helpers.KubeInformersForNamespaces

	// CRD client
	ExtensionClientSet apiextclient.Interface
	// CRD informer
	ExtensionInformer apiextinformers.SharedInformerFactory

	// operator.openshift.io client
	OperatorClientSet opclient.Interface
	// operator.openshift.io informers
	OperatorInformers opinformers.SharedInformerFactory

	// config.openshift.io client
	ConfigClientSet cfgclientset.Interface
	// config.openshift.io informers
	ConfigInformers cfginformers.SharedInformerFactory

	// Client for talking using prometheus-operator APIs (ServiceMonitor)
	MonitoringClient promclient.Interface
	// informer for prometheus-operator APIs
	MonitoringInformer prominformer.SharedInformerFactory

	// Dynamic client for OLM and old CSI operator APIs
	DynamicClient dynamic.Interface
}

const (
	OperatorNamespace    = "openshift-cluster-storage-operator"
	CSIOperatorNamespace = "openshift-cluster-csi-drivers"
	CloudConfigNamespace = "openshift-config"
)

var (
	informerNamespaces = []string{
		"", // For non-namespaced objects
		OperatorNamespace,
		CSIOperatorNamespace,
		CloudConfigNamespace,
	}
)

func NewClients(controllerConfig *controllercmd.ControllerContext, resync time.Duration) (*Clients, error) {
	c := &Clients{}
	var err error
	// Kubernetes client, used to manipulate StorageClasses
	c.KubeClient, err = kubernetes.NewForConfig(controllerConfig.ProtoKubeConfig)
	if err != nil {
		return nil, err
	}

	c.KubeInformers = v1helpers.NewKubeInformersForNamespaces(
		c.KubeClient,
		informerNamespaces...)

	c.DynamicClient, err = dynamic.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}

	// operator.openshift.io client, used to manipulate the operator CR
	c.OperatorClientSet, err = opclient.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.OperatorInformers = opinformers.NewSharedInformerFactory(c.OperatorClientSet, resync)

	// config.openshift.io client, used to get Infrastructure
	c.ConfigClientSet, err = cfgclientset.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.ConfigInformers = cfginformers.NewSharedInformerFactory(c.ConfigClientSet, resync)

	// CRD client, used to list CRDs
	c.ExtensionClientSet, err = apiextclient.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.ExtensionInformer = apiextinformers.NewSharedInformerFactory(c.ExtensionClientSet, resync)

	c.MonitoringClient, err = promclient.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.MonitoringInformer = prominformer.NewSharedInformerFactory(c.MonitoringClient, resync)

	c.OperatorClient = &operatorclient.OperatorClient{
		Informers: c.OperatorInformers,
		Client:    c.OperatorClientSet,
	}
	return c, nil
}

func StartInformers(clients *Clients, stopCh <-chan struct{}) {
	for _, informer := range []interface {
		Start(stopCh <-chan struct{})
	}{
		clients.KubeInformers,
		clients.OperatorInformers,
		clients.ConfigInformers,
		clients.ExtensionInformer,
		clients.MonitoringInformer,
	} {
		informer.Start(stopCh)
	}
}
