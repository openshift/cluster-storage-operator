#!/usr/bin/env bash

./hack/generate-manifests.sh

if [[ -n $(git status -s assets/) ]]; then
    echo 'ERROR: generated kustomize manifests needs to be updated. You can use make update to update them'
    git diff
    exit -1
fi
exit 0

