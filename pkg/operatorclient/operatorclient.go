package operatorclient

import (
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/client-go/dynamic/dynamicinformer"
)

const (
	GlobalConfigName = "cluster"
)

type OperatorClient struct {
	Informers dynamicinformer.DynamicSharedInformerFactory
	Client    v1helpers.OperatorClient
}
