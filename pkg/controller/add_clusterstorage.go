package controller

import (
	"github.com/openshift/cluster-storage-operator/pkg/controller/clusterstorage"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, clusterstorage.Add)
}
