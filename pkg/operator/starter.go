package operator

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/operator/defaultstorageclass"
	"github.com/openshift/cluster-storage-operator/pkg/operator/snapshotcrd"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	opclient "github.com/openshift/client-go/operator/clientset/versioned"
	opinformers "github.com/openshift/client-go/operator/informers/externalversions"

	cfgclientset "github.com/openshift/client-go/config/clientset/versioned"
	cfginformers "github.com/openshift/client-go/config/informers/externalversions"

	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/status"
)

const (
	resync = 20 * time.Minute
)

const (
	operatorNamespace   = "openshift-cluster-storage-operator"
	clusterOperatorName = "storage"
)

func RunOperator(ctx context.Context, controllerConfig *controllercmd.ControllerContext) error {
	kubeClient, err := kubernetes.NewForConfig(controllerConfig.ProtoKubeConfig)
	if err != nil {
		return err
	}
	kubeInformers := informers.NewSharedInformerFactory(kubeClient, resync)

	operatorClientSet, err := opclient.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return err
	}
	operatorInformers := opinformers.NewSharedInformerFactoryWithOptions(operatorClientSet, resync,
		opinformers.WithTweakListOptions(singleNameListOptions(operatorclient.GlobalConfigName)),
	)

	cfgClientset, err := cfgclientset.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return err
	}
	cfgInformers := cfginformers.NewSharedInformerFactoryWithOptions(cfgClientset, resync)

	apiExtClientset, err := apiextclient.NewForConfig(controllerConfig.KubeConfig)
	if err != nil {
		return err
	}
	apiExtInformers := apiextinformers.NewSharedInformerFactoryWithOptions(apiExtClientset, resync)

	operatorClient := &operatorclient.OperatorClient{
		Informers: operatorInformers,
		Client:    operatorClientSet,
	}

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	storageClassController := defaultstorageclass.NewController(
		operatorClient,
		kubeClient,
		kubeInformers,
		cfgInformers,
		controllerConfig.EventRecorder,
	)

	snapshotCRDController := snapshotcrd.NewController(
		operatorClient,
		apiExtInformers,
		controllerConfig.EventRecorder,
	)

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		clusterOperatorName,
		[]configv1.ObjectReference{
			{Resource: "namespaces", Name: operatorNamespace},
			{Group: operatorv1.GroupName, Resource: "storages", Name: operatorclient.GlobalConfigName},
		},
		cfgClientset.ConfigV1(),
		cfgInformers.Config().V1().ClusterOperators(),
		operatorClient,
		versionGetter,
		controllerConfig.EventRecorder,
	)

	// This controller syncs CR.Status.Conditions with the value in the field CR.Spec.ManagementStatus. It only supports Managed state
	managementStateController := management.NewOperatorManagementStateController(clusterOperatorName, operatorClient, controllerConfig.EventRecorder)
	management.SetOperatorNotRemovable()

	// This controller syncs the operator log level with the value set in the CR.Spec.OperatorLogLevel
	logLevelController := loglevel.NewClusterOperatorLoggingController(operatorClient, controllerConfig.EventRecorder)

	klog.Info("Starting the Informers.")
	for _, informer := range []interface {
		Start(stopCh <-chan struct{})
	}{
		kubeInformers,
		operatorInformers,
		cfgInformers,
		apiExtInformers,
	} {
		informer.Start(ctx.Done())
	}

	klog.Info("Starting the controllers")
	for _, controller := range []interface {
		Run(ctx context.Context, workers int)
	}{
		logLevelController,
		clusterOperatorStatus,
		managementStateController,
		storageClassController,
		snapshotCRDController,
	} {
		go controller.Run(ctx, 1)
	}

	<-ctx.Done()

	return fmt.Errorf("stopped")
}

func singleNameListOptions(name string) func(opts *metav1.ListOptions) {
	return func(opts *metav1.ListOptions) {
		opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", name).String()
	}
}
