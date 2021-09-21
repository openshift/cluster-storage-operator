module github.com/openshift/cluster-storage-operator

go 1.16

require (
	github.com/google/go-cmp v0.5.5
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20210831091943-07e756545ac1
	github.com/openshift/build-machinery-go v0.0.0-20210806203541-4ea9b6da3a37
	github.com/openshift/client-go v0.0.0-20210831095141-e19a065e79f7
	github.com/openshift/library-go v0.0.0-20210830145332-4a9873bf5e74
	github.com/prometheus-operator/prometheus-operator v0.44.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.44.1
	github.com/prometheus/client_golang v1.11.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.22.1
	k8s.io/apiextensions-apiserver v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.22.1
	k8s.io/klog/v2 v2.10.0
)

replace (
	github.com/openshift/library-go => github.com/adambkaplan/library-go v0.0.0-20210913190501-63bfdacd4f1b
	k8s.io/client-go => k8s.io/client-go v0.22.1
)
