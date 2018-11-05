FROM alpine:3.6

RUN adduser -D cluster-storage-operator
USER cluster-storage-operator

COPY deploy/deployment.yaml /manifests/deployment.yaml
COPY deploy/roles.yaml /manifests/roles.yaml
COPY deploy/image-references /manifests/image-references

ADD build/_output/bin/cluster-storage-operator /usr/local/bin/cluster-storage-operator
