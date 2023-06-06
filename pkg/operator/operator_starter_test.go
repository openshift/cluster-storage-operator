package operator

import (
	"context"
	"testing"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestHypershiftStarter(t *testing.T) {
	hsr := newFakeHyperShiftStarter()
	hsr.disableControllerStart = true
	hsr.StartOperator(context.TODO())
	if len(hsr.controllers) != 6 {
		t.Errorf("expected 5 controllers got %d", len(hsr.controllers))
	}
}

func TestStandAloneStarter(t *testing.T) {
	ssr := newFakeStandAloneStarter()
	ssr.disableControllerStart = true
	ssr.StartOperator(context.TODO())

	if len(ssr.controllers) != 5 {
		t.Errorf("unexpected controllers: %d", len(ssr.controllers))
	}
}

func newFakeStandAloneStarter() *StandaloneStarter {
	initialObjects := &csoclients.FakeTestObjects{}
	clients := csoclients.NewFakeClients(initialObjects)

	ssr := &StandaloneStarter{}
	ssr.commonClients = clients
	ssr.clientsInitialized = true

	ssr.eventRecorder = events.NewInMemoryRecorder("driver-starter")
	ssr.controllerConfig = &controllercmd.ControllerContext{
		OperatorNamespace: operatorNamespace,
	}
	return ssr
}

func newFakeHyperShiftStarter() *HyperShiftStarter {
	initialObjects := &csoclients.FakeTestObjects{}
	initialObjects.OperatorObjects = append(initialObjects.OperatorObjects, csoclients.GetCR())
	clients := csoclients.NewFakeClients(initialObjects)
	mgmtClients := csoclients.NewFakeMgmtClients(initialObjects)

	controllerContext := controllercmd.ControllerContext{
		OperatorNamespace: operatorNamespace,
	}
	hsr := &HyperShiftStarter{}
	hsr.controllerConfig = &controllerContext
	hsr.guestKubeConfig = "foobar"
	hsr.commonClients = clients
	hsr.mgmtClient = mgmtClients
	hsr.clientsInitialized = true

	eventRecorder := events.NewInMemoryRecorder("driver-starter")
	hsr.eventRecorder = eventRecorder
	return hsr
}
