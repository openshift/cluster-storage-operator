FROM registry.svc.ci.openshift.org/openshift/release:golang-1.10 AS builder
WORKDIR /go/src/github.com/openshift/cluster-storage-operator
COPY . .
RUN make build

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/cluster-storage-operator/manager /usr/bin/cluster-storage-operator
COPY manifests /manifests
RUN useradd cluster-storage-operator
USER cluster-storage-operator
ENTRYPOINT ["/usr/bin/cluster-storage-operator"]
LABEL io.openshift.release.operator true

LABEL io.k8s.display-name="OpenShift cluster-storage-operator" \
      io.k8s.description="This is a component of OpenShift Container Platform and manages the lifecycle of cluster storage components." \
      maintainer="Matthew Wong <mawong@redhat.com>"
