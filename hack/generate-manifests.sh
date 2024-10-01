#!/usr/bin/env bash

drivers=( aws-ebs azure-disk azure-file openstack-cinder openstack-manila )

for driver in "${drivers[@]}"; do
    # Ignore drivers that don't (yet) support HyperShift
    if [ -d "assets/csidriveroperators/${driver}/hypershift" ]; then
        rm -rf "assets/csidriveroperators/${driver}/hypershift/guest/generated"
        mkdir -p "assets/csidriveroperators/${driver}/hypershift/guest/generated"
        oc kustomize \
            "assets/csidriveroperators/${driver}/hypershift/guest" \
            -o "assets/csidriveroperators/${driver}/hypershift/guest/generated"

        rm -rf "assets/csidriveroperators/${driver}/hypershift/mgmt/generated"
        mkdir -p "assets/csidriveroperators/${driver}/hypershift/mgmt/generated"
        oc kustomize \
            "assets/csidriveroperators/${driver}/hypershift/mgmt" \
            -o "assets/csidriveroperators/${driver}/hypershift/mgmt/generated"
    fi

    rm -rf "assets/csidriveroperators/${driver}/standalone/generated"
    mkdir -p "assets/csidriveroperators/${driver}/standalone/generated"
    oc kustomize \
        "assets/csidriveroperators/${driver}/standalone" \
        -o "assets/csidriveroperators/${driver}/standalone/generated"
done
