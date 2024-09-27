#!/usr/bin/env bash

drivers=( aws-ebs azure-disk azure-file )

for driver in "${drivers[@]}"; do
    oc kustomize \
        "assets/csidriveroperators/${driver}/hypershift/guest" \
        -o "assets/csidriveroperators/${driver}/hypershift/guest/generated"

    oc kustomize \
        "assets/csidriveroperators/${driver}/hypershift/mgmt" \
        -o "assets/csidriveroperators/${driver}/hypershift/mgmt/generated"

    oc kustomize \
        "assets/csidriveroperators/${driver}/standalone" \
        -o "assets/csidriveroperators/${driver}/standalone/generated"
done
