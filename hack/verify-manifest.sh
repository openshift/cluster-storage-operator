#!/usr/bin/env bash

./hack/generate-manifests.sh

if [[ -n $(git status -s assets/) ]]; then
    echo 'Assets has been modified and/or untracked'
    exit -1
fi
exit 0

