package csoclients

import (
	"fmt"
	"time"

	"github.com/openshift/library-go/pkg/config/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/utils/clock"

	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"

	operatorv1 "github.com/openshift/api/operator/v1"
	cfgclientset "github.com/openshift/client-go/config/clientset/versioned"
	cfginformers "github.com/openshift/client-go/config/informers/externalversions"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	opinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	prominformer "github.com/prometheus-operator/prometheus-operator/pkg/client/informers/externalversions"
	promclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
)

type Clients struct {
	// Client for CSO's CR
	OperatorClient          v1helpers.OperatorClientWithFinalizers
	OperatorClientInformers dynamicinformer.DynamicSharedInformerFactory

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

	// Dynamic client for old CSI operator APIs and HyperShift
	DynamicClient   dynamic.Interface
	DynamicInformer dynamicinformer.DynamicSharedInformerFactory

	// Rest Mapper for mapping GVK to GVR
	RestMapper       *restmapper.DeferredDiscoveryRESTMapper
	CategoryExpander restmapper.CategoryExpander
}

const (
	OperatorNamespace      = "openshift-cluster-storage-operator"
	CSIOperatorNamespace   = "openshift-cluster-csi-drivers"
	CloudConfigNamespace   = "openshift-config"
	ManagedConfigNamespace = "openshift-config-managed"
)

var (
	informerNamespaces = []string{
		"", // For non-namespaced objects
		OperatorNamespace,
		CSIOperatorNamespace,
		CloudConfigNamespace,
		ManagedConfigNamespace,
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
	c.DynamicInformer = dynamicinformer.NewDynamicSharedInformerFactory(c.DynamicClient, resync)

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

	c.OperatorClient, c.OperatorClientInformers, err = genericoperatorclient.NewClusterScopedOperatorClient(
		clock.RealClock{},
		controllerConfig.KubeConfig,
		operatorv1.GroupVersion.WithResource("storages"),
		operatorv1.GroupVersion.WithKind("Storage"),
		extractOperatorSpec,
		extractOperatorStatus,
	)
	if err != nil {
		return nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.RestMapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	c.CategoryExpander = restmapper.NewDiscoveryCategoryExpander(dc)

	return c, nil
}

func NewHypershiftMgmtClients(controllerConfig *controllercmd.ControllerContext, controlNamespace string, resync time.Duration) (*Clients, error) {
	c := &Clients{}
	var err error
	// Kubernetes client, used to manipulate StorageClasses
	c.KubeClient, err = kubernetes.NewForConfig(controllerConfig.ProtoKubeConfig)
	if err != nil {
		return nil, err
	}

	c.KubeInformers = v1helpers.NewKubeInformersForNamespaces(c.KubeClient, controlNamespace)

	c.DynamicClient, err = dynamic.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.DynamicInformer = dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.DynamicClient, resync, controlNamespace, nil)

	// config.openshift.io client, used to get Infrastructure
	c.ConfigClientSet, err = cfgclientset.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.ConfigInformers = cfginformers.NewSharedInformerFactory(c.ConfigClientSet, resync)

	dc, err := discovery.NewDiscoveryClientForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return nil, err
	}
	c.RestMapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	c.CategoryExpander = restmapper.NewDiscoveryCategoryExpander(dc)

	return c, nil
}

func NewHypershiftGuestClients(
	controllerConfig *controllercmd.ControllerContext,
	guestKubeConfig string,
	controllerName string, resync time.Duration) (*Clients, error) {
	c := &Clients{}
	var err error
	kubeRestConfig, err := client.GetKubeConfigOrInClusterConfig(guestKubeConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to use guest kubeconfig %s: %s", guestKubeConfig, err)
	}
	// TODO set user agent name here
	guestKubeClient := kubernetes.NewForConfigOrDie(rest.AddUserAgent(kubeRestConfig, controllerName))
	// Kubernetes client, used to manipulate StorageClasses
	c.KubeClient = guestKubeClient
	if err != nil {
		return nil, err
	}

	c.KubeInformers = v1helpers.NewKubeInformersForNamespaces(
		c.KubeClient,
		informerNamespaces...)

	c.DynamicClient, err = dynamic.NewForConfig(kubeRestConfig)
	if err != nil {
		return nil, err
	}
	c.DynamicInformer = dynamicinformer.NewDynamicSharedInformerFactory(c.DynamicClient, resync)

	// operator.openshift.io client, used to manipulate the operator CR
	c.OperatorClientSet, err = opclient.NewForConfig(kubeRestConfig)
	if err != nil {
		return nil, err
	}
	c.OperatorInformers = opinformers.NewSharedInformerFactory(c.OperatorClientSet, resync)

	// config.openshift.io client, used to get Infrastructure
	c.ConfigClientSet, err = cfgclientset.NewForConfig(kubeRestConfig)
	if err != nil {
		return nil, err
	}
	c.ConfigInformers = cfginformers.NewSharedInformerFactory(c.ConfigClientSet, resync)

	// CRD client, used to list CRDs
	c.ExtensionClientSet, err = apiextclient.NewForConfig(kubeRestConfig)
	if err != nil {
		return nil, err
	}
	c.ExtensionInformer = apiextinformers.NewSharedInformerFactory(c.ExtensionClientSet, resync)

	c.MonitoringClient, err = promclient.NewForConfig(kubeRestConfig)
	if err != nil {
		return nil, err
	}
	c.MonitoringInformer = prominformer.NewSharedInformerFactory(c.MonitoringClient, resync)

	c.OperatorClient, c.OperatorClientInformers, err = genericoperatorclient.NewClusterScopedOperatorClient(
		clock.RealClock{},
		controllerConfig.KubeConfig,
		operatorv1.GroupVersion.WithResource("storages"),
		operatorv1.GroupVersion.WithKind("Storage"),
		extractOperatorSpec,
		extractOperatorStatus,
	)
	if err != nil {
		return nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(kubeRestConfig)
	if err != nil {
		return nil, err
	}
	c.RestMapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	c.CategoryExpander = restmapper.NewDiscoveryCategoryExpander(dc)
	return c, nil
}

func StartInformers(clients *Clients, stopCh <-chan struct{}) {
	for _, informer := range []interface {
		Start(stopCh <-chan struct{})
	}{
		clients.KubeInformers,
		clients.OperatorClientInformers,
		clients.OperatorInformers,
		clients.ConfigInformers,
		clients.ExtensionInformer,
		clients.MonitoringInformer,
		clients.DynamicInformer,
	} {
		informer.Start(stopCh)
	}
}

func StartGuestInformers(clients *Clients, stopCh <-chan struct{}) {
	for _, informer := range []interface {
		Start(stopCh <-chan struct{})
	}{
		clients.KubeInformers,
		clients.OperatorClientInformers,
		clients.OperatorInformers,
		clients.ConfigInformers,
		clients.ExtensionInformer,
		clients.MonitoringInformer,
		clients.DynamicInformer,
	} {
		informer.Start(stopCh)
	}
}

func StartMgmtInformers(clients *Clients, stopCh <-chan struct{}) {
	for _, informer := range []interface {
		Start(stopCh <-chan struct{})
	}{
		clients.KubeInformers,
		clients.OperatorClientInformers,
		clients.ConfigInformers,
		clients.DynamicInformer,
	} {
		informer.Start(stopCh)
	}
}

func extractOperatorSpec(obj *unstructured.Unstructured, fieldManager string) (*applyoperatorv1.OperatorSpecApplyConfiguration, error) {
	castObj := &operatorv1.Storage{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, castObj); err != nil {
		return nil, fmt.Errorf("unable to convert to Storage: %w", err)
	}
	ret, err := applyoperatorv1.ExtractStorage(castObj, fieldManager)
	if err != nil {
		return nil, fmt.Errorf("unable to extract fields for %q: %w", fieldManager, err)
	}
	if ret.Spec == nil {
		return nil, nil
	}
	return &ret.Spec.OperatorSpecApplyConfiguration, nil
}

func extractOperatorStatus(obj *unstructured.Unstructured, fieldManager string) (*applyoperatorv1.OperatorStatusApplyConfiguration, error) {
	castObj := &operatorv1.Storage{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, castObj); err != nil {
		return nil, fmt.Errorf("unable to convert to Storage: %w", err)
	}
	ret, err := applyoperatorv1.ExtractStorageStatus(castObj, fieldManager)
	if err != nil {
		return nil, fmt.Errorf("unable to extract fields for %q: %w", fieldManager, err)
	}

	if ret.Status == nil {
		return nil, nil
	}
	return &ret.Status.OperatorStatusApplyConfiguration, nil
}
