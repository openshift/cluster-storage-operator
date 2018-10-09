FROM alpine:3.6

RUN adduser -D cluster-storage-operator
USER cluster-storage-operator

ADD tmp/_output/bin/cluster-storage-operator /usr/local/bin/cluster-storage-operator
