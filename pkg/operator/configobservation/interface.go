package configobservation

import (
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"k8s.io/client-go/tools/cache"
)

// Listers implement the configobserver.Listers interface.
type Listers struct {
	ProxyLister_ configlistersv1.ProxyLister

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) ProxyLister() configlistersv1.ProxyLister {
	return l.ProxyLister_
}

func (l Listers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return l.ResourceSync
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}
