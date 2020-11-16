module github.com/openshift/cluster-storage-operator

go 1.14

require (
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/google/go-cmp v0.4.0
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20201110171212-31aac52f4998
	github.com/openshift/build-machinery-go v0.0.0-20200917070002-f171684f77ab
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/library-go v0.0.0-20200909144351-f29d76719396
	github.com/prometheus/client_golang v1.7.1
	github.com/spf13/cobra v1.0.0
	github.com/spf13/pflag v1.0.5
	golang.org/x/net v0.0.0-20200813134508-3edf25e44fcc // indirect
	k8s.io/api v0.19.2
	k8s.io/apiextensions-apiserver v0.19.0
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.0
	k8s.io/code-generator v0.19.2
	k8s.io/component-base v0.19.0
	k8s.io/klog/v2 v2.3.0
)
