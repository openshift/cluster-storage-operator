IMG ?= openshift/origin-cluster-storage-operator:latest

PACKAGE=github.com/openshift/cluster-storage-operator
MAIN_PACKAGE=$(PACKAGE)/cmd/cluster-storage-operator

BIN=$(lastword $(subst /, ,$(MAIN_PACKAGE)))

ENVVAR=GOOS=linux CGO_ENABLED=0
GOOS=linux
GO_BUILD_RECIPE=GOOS=$(GOOS) go build -o $(BIN) $(MAIN_PACKAGE)

BINDATA=pkg/generated/bindata.go
GOBINDATA_BIN=$(GOPATH)/bin/go-bindata

all: build

build: generate
	$(GO_BUILD_RECIPE)

generate: $(GOBINDATA_BIN)
	$(GOBINDATA_BIN) -nometadata -pkg generated -o $(BINDATA) assets/...

$(GOBINDATA_BIN):
	go build -o $(GOBINDATA_BIN) ./vendor/github.com/jteeuwen/go-bindata/go-bindata

test:
	go test ./pkg/...

verify:
	hack/verify-gofmt.sh
	hack/verify-gometalinter.sh
	hack/verify-govet.sh

container: build test verify
	docker build . -t $(IMG)

clean:
	go clean
	rm -f $(BIN)
