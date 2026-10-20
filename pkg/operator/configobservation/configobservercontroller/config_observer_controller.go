package configobservercontroller

import (
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	libgoapiserver "github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
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
		clients.ConfigInformers.Config().V1().APIServers().Informer(),
	}

	c := &ConfigObserverController{
		Controller: configobserver.NewConfigObserver(
			"openshift-storage",
			clients.OperatorClient,
			eventRecorder.WithComponentSuffix("config-observer-controller-"),
			configobservation.Listers{
				ProxyLister_:     clients.ConfigInformers.Config().V1().Proxies().Lister(),
				APIServerLister_: clients.ConfigInformers.Config().V1().APIServers().Lister(),
				PreRunCachesSynced: append([]cache.InformerSynced{},
					clients.OperatorClient.Informer().HasSynced,
					clients.ConfigInformers.Config().V1().Proxies().Informer().HasSynced,
					clients.ConfigInformers.Config().V1().APIServers().Informer().HasSynced,
				),
			},
			informers,
			proxy.NewProxyObserveFunc(util.ProxyConfigPath()),
			observeTLSSecurityProfile,
		),
	}

	return c
}

func observeTLSSecurityProfile(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	return libgoapiserver.ObserveTLSSecurityProfileWithPaths(genericListers, recorder, existingConfig, []string{"targetconfig", "servingInfo", "minTLSVersion"}, []string{"targetconfig", "servingInfo", "cipherSuites"})
}
