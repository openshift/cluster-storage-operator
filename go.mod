module github.com/openshift/cluster-storage-operator

go 1.15

require (
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/go-cmp v0.5.2
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20210329131242-b54945f3bcd8
	github.com/openshift/build-machinery-go v0.0.0-20200917070002-f171684f77ab
	github.com/openshift/client-go v0.0.0-20201214125552-e615e336eb49
	github.com/openshift/library-go v0.0.0-20210113192829-cfbb3f4c80c2
	github.com/prometheus-operator/prometheus-operator v0.44.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.44.1
	github.com/prometheus/client_golang v1.8.0
	github.com/spf13/cobra v1.1.1
	github.com/spf13/pflag v1.0.5
	golang.org/x/net v0.0.0-20210326220855-61e056675ecf // indirect
	golang.org/x/text v0.3.5 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/api v0.20.5
	k8s.io/apiextensions-apiserver v0.20.4
	k8s.io/apimachinery v0.20.5
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/code-generator v0.20.4
	k8s.io/component-base v0.20.4
	k8s.io/klog/v2 v2.8.0
	sigs.k8s.io/structured-merge-diff/v4 v4.1.0 // indirect
)

replace (
	google.golang.org/grpc => google.golang.org/grpc v1.27.0
	k8s.io/client-go => k8s.io/client-go v0.20.4
)
