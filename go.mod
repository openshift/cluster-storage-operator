module github.com/openshift/cluster-storage-operator

go 1.16

require (
	github.com/google/go-cmp v0.5.5
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20211018182944-3a31a0369345
	github.com/openshift/build-machinery-go v0.0.0-20211213093930-7e33a7eb4ce3
	github.com/openshift/client-go v0.0.0-20211014121513-e0d04d36b53a
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

replace k8s.io/client-go => k8s.io/client-go v0.22.1
