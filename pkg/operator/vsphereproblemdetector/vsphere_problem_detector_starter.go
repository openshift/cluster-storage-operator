package vsphereproblemdetector

import (
	"context"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorapi "github.com/openshift/api/operator/v1"
	openshiftv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/generated"
	"github.com/openshift/cluster-storage-operator/pkg/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/controller/manager"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"k8s.io/klog/v2"
)

const (
	infraConfigName = "cluster"
)

type VSphereProblemDetectorStarter struct {
	controller     manager.ControllerManager
	operatorClient *operatorclient.OperatorClient
	infraLister    openshiftv1.InfrastructureLister
	versionGetter  status.VersionGetter
	targetVersion  string
	eventRecorder  events.Recorder
	running        bool
}

func NewVSphereProblemDetectorStarter(
	clients *csoclients.Clients,
	resyncInterval time.Duration,
	versionGetter status.VersionGetter,
	targetVersion string,
	eventRecorder events.Recorder) factory.Controller {
	c := &VSphereProblemDetectorStarter{
		operatorClient: clients.OperatorClient,
		infraLister:    clients.ConfigInformers.Config().V1().Infrastructures().Lister(),
		versionGetter:  versionGetter,
		targetVersion:  targetVersion,
		eventRecorder:  eventRecorder.WithComponentSuffix("VSphereProblemDetectorStarter"),
	}
	c.controller = c.createVSphereProblemDetectorManager(clients, resyncInterval)
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.ConfigInformers.Config().V1().Infrastructures().Informer(),
	).ToController("VSphereProblemDetectorStarter", eventRecorder)
}

func (c *VSphereProblemDetectorStarter) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("VSphereProblemDetectorStarter.Sync started")
	defer klog.V(4).Infof("VSphereProblemDetectorStarter.Sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	infrastructure, err := c.infraLister.Get(infraConfigName)
	if err != nil {
		return err
	}

	// Start controller managers for this platform
	var platform configv1.PlatformType
	if infrastructure.Status.PlatformStatus != nil {
		platform = infrastructure.Status.PlatformStatus.Type
	}

	// if not vsphere turn without any error
	if platform != configv1.VSpherePlatformType {
		return nil
	}

	if !c.running {
		go c.controller.Start(ctx)
		c.running = true
	}
	return nil
}

func (c *VSphereProblemDetectorStarter) createVSphereProblemDetectorManager(
	clients *csoclients.Clients,
	resyncInterval time.Duration) manager.ControllerManager {
	mgr := manager.NewControllerManager()

	staticAssets := []string{
		"vsphere_problem_detector/01_sa.yaml",
		"vsphere_problem_detector/02_role.yaml",
		"vsphere_problem_detector/03_rolebinding.yaml",
		"vsphere_problem_detector/04_clusterrole.yaml",
		"vsphere_problem_detector/05_clusterrolebinding.yaml",
		"vsphere_problem_detector/10_service.yaml",
	}

	mgr = mgr.WithController(staticresourcecontroller.NewStaticResourceController(
		"VSphereProblemDetectorStarterStaticController",
		generated.Asset,
		staticAssets,
		resourceapply.NewKubeClientHolder(clients.KubeClient),
		c.operatorClient,
		c.eventRecorder).AddKubeInformers(clients.KubeInformers), 1)

	mgr = mgr.WithController(NewVSphereProblemDetectorDeploymentController(
		clients,
		c.versionGetter,
		c.targetVersion,
		c.eventRecorder,
		resyncInterval), 1)

	mgr = mgr.WithController(newMonitoringController(
		clients,
		c.eventRecorder,
		resyncInterval), 1)

	return mgr
}
