package operator

import (
	"context"
	"time"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

const (
	resync = 20 * time.Minute
)

const (
	operatorNamespace   = "openshift-cluster-storage-operator"
	clusterOperatorName = "storage"
)

func RunOperator(ctx context.Context, controllerConfig *controllercmd.ControllerContext, guestKubeConfig *string) error {
	isHyperShift := false
	if guestKubeConfig != nil && *guestKubeConfig != "" {
		isHyperShift = true
	}

	starter := NewStandaloneStarter(controllerConfig)

	if isHyperShift {
		starter = NewHyperShiftStarter(controllerConfig, *guestKubeConfig)
	}
	return starter.StartOperator(ctx)
}
