all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/deps-gomod.mk \
	targets/openshift/images.mk \
	targets/openshift/operator/profile-manifests.mk \
)

# Run core verification and all self contained tests.
#
# Example:
#   make check
check: | verify test-unit
.PHONY: check

# Select the target release's image explicitly, or set PROMTOOL for native tests.
PROMETHEUS_IMAGE ?=
CONTAINER_RUNTIME ?= podman
PROMETHEUS_CONTAINER_SECURITY_OPT ?= label=disable
PROMTOOL ?=

verify: verify-prometheus-rules

verify-prometheus-rules:
	PROMTOOL="$(PROMTOOL)" \
	PROMETHEUS_IMAGE="$(PROMETHEUS_IMAGE)" \
	CONTAINER_RUNTIME="$(CONTAINER_RUNTIME)" \
	PROMETHEUS_CONTAINER_SECURITY_OPT="$(PROMETHEUS_CONTAINER_SECURITY_OPT)" \
	./hack/test-prometheus-rules.sh
.PHONY: verify-prometheus-rules

IMAGE_REGISTRY?=registry.svc.ci.openshift.org

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
# It will generate target "image-$(1)" for building the image and binding it as a prerequisite to target "images".
$(call build-image,cluster-storage-operator,$(IMAGE_REGISTRY)/ocp/4.6:cluster-storage-operator,./Dockerfile.rhel7,.)

# add targets to manage profile patches
# $0 - macro name
# $1 - target name
# $2 - patches directory
# $3 - manifests directory
$(call add-profile-manifests,manifests,./profile-patches,./manifests)

update: generate-kustomize-manifests update-generated

generate-kustomize-manifests:
	./hack/generate-manifests.sh

clean:
	$(RM) cluster-storage-operator
.PHONY: clean

GO_TEST_PACKAGES :=./pkg/... ./cmd/...
