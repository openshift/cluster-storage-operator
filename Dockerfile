FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.21-openshift-4.17 AS builder
WORKDIR /go/src/github.com/openshift/cluster-storage-operator
COPY . .
RUN make

FROM registry.ci.openshift.org/ocp/4.17:base-rhel9
COPY --from=builder /go/src/github.com/openshift/cluster-storage-operator/cluster-storage-operator /usr/bin/
COPY manifests /manifests
COPY vendor/github.com/openshift/api/operator/v1/zz_generated.crd-manifests/0000_50_storage_01_storages.crd.yaml manifests/05_crd_operator.yaml
COPY vendor/github.com/openshift/api/operator/v1/zz_generated.crd-manifests/0000_90_csi-driver_01_clustercsidrivers-CustomNoUpgrade.crd.yaml manifests/04_cluster_csi_driver_crd-CustomNoUpgrade.yaml
COPY vendor/github.com/openshift/api/operator/v1/zz_generated.crd-manifests/0000_90_csi-driver_01_clustercsidrivers-Default.crd.yaml manifests/04_cluster_csi_driver_crd-Default.yaml
COPY vendor/github.com/openshift/api/operator/v1/zz_generated.crd-manifests/0000_90_csi-driver_01_clustercsidrivers-TechPreviewNoUpgrade.crd.yaml manifests/04_cluster_csi_driver_crd-TechPreviewNoUpgrade.yaml
ENTRYPOINT ["/usr/bin/cluster-storage-operator"]
LABEL io.openshift.release.operator true
LABEL io.k8s.display-name="OpenShift Cluster Storage Operator" \
      io.k8s.description="The cluster-storage-operator installs and maintains the storage components of OCP cluster."
