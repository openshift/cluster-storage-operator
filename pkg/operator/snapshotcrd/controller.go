package snapshotcrd

import (
	"context"
	"fmt"
	"strings"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	v1 "k8s.io/apiextensions-apiserver/pkg/client/listers/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

const (
	conditionsPrefix = "SnapshotCRDController"

	alphaVersion           = "v1alpha1"
	snapshotCRDName        = "volumesnapshots.snapshot.storage.k8s.io"
	snapshotClassCRDName   = "volumesnapshotclasses.snapshot.storage.k8s.io"
	snapshotContentCRDName = "volumesnapshotcontents.snapshot.storage.k8s.io"
)

// This Controller checks for presence of v1alpha1 VolumeSnapshot CRDs
// and marks the cluster Upgradeable=false when they're found.
// It produces following Conditions:
// SnapshotCRDControllerUpgradeable: v1alpha1 VolumeSnapshot CRDs are not
//    present.
// SnapshotCRDControllerDegraded - error checking for CRDs.
type Controller struct {
	operatorClient v1helpers.OperatorClient
	crdLister      v1.CustomResourceDefinitionLister
	eventRecorder  events.Recorder
}

func NewController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder) factory.Controller {
	c := &Controller{
		operatorClient: clients.OperatorClient,
		crdLister:      clients.ExtensionInformer.Apiextensions().V1().CustomResourceDefinitions().Lister(),
		eventRecorder:  eventRecorder,
	}
	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(clients.OperatorClient).WithInformers(
		clients.OperatorClient.Informer(),
		clients.ExtensionInformer.Apiextensions().V1().CustomResourceDefinitions().Informer(),
	).ToController("SnapshotCRDController", eventRecorder)
}

func (c *Controller) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.V(4).Infof("SnapshotCRDController sync started")
	defer klog.V(4).Infof("SnapshotCRDController sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorapi.Managed {
		return nil
	}

	alphaCRDs, err := c.hasAlphaCRDs()
	if err != nil {
		// Will set SnapshotCRDControllerDegraded = true
		return err
	}

	upgradeable := operatorapi.OperatorCondition{
		Type:   conditionsPrefix + operatorapi.OperatorStatusTypeUpgradeable,
		Status: operatorapi.ConditionTrue,
	}

	if len(alphaCRDs) > 0 {
		msg := fmt.Sprintf("Unable to update cluster as v1alpha1 version of %s is detected. Remove these CRDs to allow the upgrade to proceed.", strings.Join(alphaCRDs, ", "))
		upgradeable.Status = operatorapi.ConditionFalse
		upgradeable.Message = msg
		upgradeable.Reason = "AlphaDetected"
	}

	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient,
		v1helpers.UpdateConditionFn(upgradeable),
	); updateErr != nil {
		return updateErr
	}

	return nil
}

func (c *Controller) hasAlphaCRDs() ([]string, error) {
	crdMap := map[string]string{
		"VolumeSnapshot":        snapshotCRDName,
		"VolumeSnapshotClass":   snapshotClassCRDName,
		"VolumeSnapshotContent": snapshotContentCRDName}
	var foundCRD []string
	for shortName, fullName := range crdMap {
		crd, err := c.crdLister.Get(fullName)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, err
			}
			klog.V(4).Infof("CRD %s not found", fullName)
			continue
		}

		for _, version := range crd.Spec.Versions {
			if version.Name == alphaVersion {
				foundCRD = append(foundCRD, shortName)
				klog.Errorf("Found %s CRD %s", alphaVersion, fullName)
			}
		}
	}

	if len(foundCRD) > 0 {
		return foundCRD, nil
	}
	return nil, nil
}
