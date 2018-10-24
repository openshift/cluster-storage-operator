BINDATA=pkg/generated/bindata.go
GOBINDATA_BIN=$(GOPATH)/bin/go-bindata

all: build

build: generate
	operator-sdk build quay.io/openshift/cluster-storage-operator

generate: $(GOBINDATA_BIN)
	go-bindata -nometadata -pkg generated -o $(BINDATA) manifests/...

$(GOBINDATA_BIN):
	go get -u github.com/jteeuwen/go-bindata/...

test:
	go test ./pkg/...

verify:
	hack/verify-gofmt.sh
	hack/verify-gometalinter.sh
	hack/verify-govet.sh

clean:
	rm -rf _output
