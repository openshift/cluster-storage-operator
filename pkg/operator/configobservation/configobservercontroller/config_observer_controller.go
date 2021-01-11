package configobservercontroller

import (
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/proxy"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-storage-operator/pkg/operator/configobservation/util"
)

// ConfigObserverController watches information that's relevant to CSO and adds
// it to CR.Spec.ObservedConfig.
type ConfigObserverController struct {
	factory.Controller
}

// NewConfigObserverController returns a new ConfigObserverController.
func NewConfigObserverController(
	clients *csoclients.Clients,
	eventRecorder events.Recorder,
) *ConfigObserverController {
	informers := []factory.Informer{
		clients.OperatorClient.Informer(),
		clients.ConfigInformers.Config().V1().Proxies().Informer(),
	}

	c := &ConfigObserverController{
		Controller: configobserver.NewConfigObserver(
			clients.OperatorClient,
			eventRecorder.WithComponentSuffix("config-observer-controller-"),
			configobservation.Listers{
				ProxyLister_: clients.ConfigInformers.Config().V1().Proxies().Lister(),
				PreRunCachesSynced: append([]cache.InformerSynced{},
					clients.OperatorClient.Informer().HasSynced,
					clients.ConfigInformers.Config().V1().Proxies().Informer().HasSynced,
				),
			},
			informers,
			proxy.NewProxyObserveFunc(util.ProxyConfigPath()),
		),
	}

	return c
}
