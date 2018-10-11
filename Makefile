# TODO https://github.com/openshift/cluster-ingress-operator/blob/master/Makefile

BINDATA=pkg/generated/bindata.go

GOBINDATA_BIN=$(GOPATH)/bin/go-bindata

# Using "-modtime 1" to make generate target deterministic. It sets all file time stamps to unix timestamp 1
generate: $(GOBINDATA_BIN)
	go-bindata -nometadata -pkg generated -o $(BINDATA) manifests/...
	# go-bindata -nometadata -pkg generated -o $(TEST_BINDATA) test/manifests/...

$(GOBINDATA_BIN):
	go get -u github.com/jteeuwen/go-bindata/...

helm:
	mkdir -p _output
	helm template deploy/olm/chart -f deploy/olm/chart/values.yaml --output-dir _output

deploy-olm: helm
	-kubectl create -f _output/olm/templates
	-kubectl create -f _output/olm/templates/30_09-rh-operators.catalogsource.yaml

deploy-subscription: deploy-olm
	kubectl create -f deploy/olm/subscription.yaml

deploy-installplan: deploy-olm
	kubectl create -f deploy/olm/installplan.yaml

deploy-vanilla:
	kubectl create -f deploy

clean:
	rm -rf _output
