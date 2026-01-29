package csioperatorclient

import (
	"github.com/openshift/cluster-storage-operator/assets"
	"k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"
)

func getCSIDriverDeploymentName(assetName string) string {
	assetBytes, err := assets.ReadFile(assetName)
	if err != nil {
		panic(err)
	}

	var deployment v1.Deployment

	err = yaml.Unmarshal(assetBytes, &deployment)
	if err != nil {
		panic(err)
	}

	return deployment.Name
}
