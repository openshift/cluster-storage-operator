module github.com/openshift/cluster-storage-operator

go 1.15

require (
	github.com/certifi/gocertifi v0.0.0-20180905225744-ee1a9a0726d2 // indirect
	github.com/containerd/continuity v0.0.0-20190827140505-75bee3e2ccb6 // indirect
	github.com/docker/libnetwork v0.0.0-20190731215715-7f13a5c99f4b // indirect
	github.com/fsouza/go-dockerclient v0.0.0-20171004212419-da3951ba2e9e // indirect
	github.com/getsentry/raven-go v0.0.0-20190513200303-c977f96e1095 // indirect
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/google/go-cmp v0.5.5
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/opencontainers/runc v0.0.0-20191031171055-b133feaeeb2e // indirect
	github.com/openshift/api v0.0.0-20210412212256-79bd8cfbbd59
	github.com/openshift/build-machinery-go v0.0.0-20210209125900-0da259a2c359
	github.com/openshift/client-go v0.0.0-20210409155308-a8e62c60e930
	github.com/openshift/library-go v0.0.0-20210408164723-7a65fdb398e2
	github.com/prometheus-operator/prometheus-operator v0.44.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.44.1
	github.com/prometheus/client_golang v1.8.0
	github.com/spf13/cobra v1.1.1
	github.com/spf13/pflag v1.0.5
	github.com/vishvananda/netlink v1.0.0 // indirect
	github.com/vishvananda/netns v0.0.0-20191106174202-0a2b9b5464df // indirect
	golang.org/x/net v0.0.0-20210410081132-afb366fc7cd1 // indirect
	k8s.io/api v0.21.0
	k8s.io/apiextensions-apiserver v0.21.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/code-generator v0.21.0
	k8s.io/component-base v0.21.0
	k8s.io/klog/v2 v2.8.0
	sigs.k8s.io/structured-merge-diff/v4 v4.1.1 // indirect
)

replace (
	google.golang.org/grpc => google.golang.org/grpc v1.27.0
	k8s.io/client-go => k8s.io/client-go v0.21.0
)
