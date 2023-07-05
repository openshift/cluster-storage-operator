#!/usr/bin/env bash

oc kustomize \
    assets/csidriveroperators/aws-ebs/hypershift/guest \
    -o assets/csidriveroperators/aws-ebs/hypershift/guest/generated

oc kustomize \
    assets/csidriveroperators/aws-ebs/hypershift/mgmt \
    -o assets/csidriveroperators/aws-ebs/hypershift/mgmt/generated
    
oc kustomize \
    assets/csidriveroperators/aws-ebs/standalone \
    -o assets/csidriveroperators/aws-ebs/standalone/generated
    




