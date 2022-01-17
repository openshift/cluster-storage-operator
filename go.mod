module github.com/openshift/cluster-storage-operator

go 1.16

require (
	github.com/go-logr/logr v1.2.2 // indirect
	github.com/google/go-cmp v0.5.6
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20220114150019-2499da51153e
	github.com/openshift/build-machinery-go v0.0.0-20211213093930-7e33a7eb4ce3
	github.com/openshift/client-go v0.0.0-20211209144617-7385dd6338e3
	github.com/openshift/library-go v0.0.0-20220114151217-4362aa519714
	github.com/prometheus-operator/prometheus-operator v0.44.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.44.1
	github.com/prometheus/client_golang v1.11.0
	github.com/spf13/cobra v1.2.1
	golang.org/x/net v0.0.0-20220114011407-0dd24b26b47d // indirect
	k8s.io/api v0.23.1
	k8s.io/apiextensions-apiserver v0.23.0
	k8s.io/apimachinery v0.23.1
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.23.0
	k8s.io/klog/v2 v2.40.1
	k8s.io/utils v0.0.0-20211208161948-7d6a63dca704 // indirect
	sigs.k8s.io/json v0.0.0-20211208200746-9f7c6b3444d2 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.1 // indirect
)

replace k8s.io/client-go => k8s.io/client-go v0.23.0
