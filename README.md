# cluster-storage-operator
Operator that sets OCP cluster-wide storage defaults.

Ensures a default storage class exists for OCP clusters, like the [addon-manager](https://github.com/kubernetes/kubernetes/tree/release-1.13/cluster/addons/storage-class) does for kubernetes clusters. Supports AWS and OpenStack. No configuration is required. The created storage class can be made non-default by editing its annotation but cannot be deleted so long as the operator runs.

Will also ensure default CSI volume plugins are installed in a future release when CSI plugins replace in-tree ones (see [csi-operator](https://github.com/openshift/csi-operator)).

## Quick start - running CSO from local workstation

### Scale down current CVO and CSO

```shell
# Set kubeconfig for connection to existing cluster
export KUBECONFIG=<path-to-kubeconfig>

# Scale down CVO and CSO
oc scale --replicas=0 deploy/cluster-version-operator -n openshift-cluster-version  
oc scale --replicas=0 deploy/cluster-storage-operator -n openshift-cluster-storage-operator
```

### Configure required environment variables

```shell
# Set operator and operand image version (this is just a marker for missing version, we assume that operators handle version detection themselves)
export OPERATOR_IMAGE_VERSION="0.0.1-snapshot"
export OPERAND_IMAGE_VERSION=$OPERATOR_IMAGE_VERSION

# Set common environment variables (CCMO and sidecars)
export CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE=quay.io/openshift/origin-cluster-cloud-controller-manager-operator:latest
export PROVISIONER_IMAGE=quay.io/openshift/origin-csi-external-provisioner:latest  
export ATTACHER_IMAGE=quay.io/openshift/origin-csi-external-attacher:latest 
export RESIZER_IMAGE=quay.io/openshift/origin-csi-external-resizer:latest  
export SNAPSHOTTER_IMAGE=quay.io/openshift/origin-csi-external-snapshotter:latest  
export NODE_DRIVER_REGISTRAR_IMAGE=quay.io/openshift/origin-csi-node-driver-registrar:latest  
export LIVENESS_PROBE_IMAGE=quay.io/openshift/origin-csi-livenessprobe:latest  
export KUBE_RBAC_PROXY_IMAGE=quay.io/openshift/origin-kube-rbac-proxy:latest  
export VOLUME_DATA_SOURCE_VALIDATOR_IMAGE=quay.io/openshift/origin-volume-data-source-validator:latest
export VOLUME_DEVICE_EXPORTER_IMAGE=quay.io/openshift/origin-csi-volume-device-exporter:latest
```

#### Configure provider specific variables

> Note that each provider requires different env variables to be set. Inspect their respective asset files to see what variables are needed and then find which env variables are used to replace them.
> Example with AWS EBS: see the asset file [here](https://github.com/openshift/cluster-storage-operator/blob/2b8e4fce4ddf3bfdd34fef5b2a4aeae4354a47e3/assets/csidriveroperators/aws-ebs/base/09_deployment.yaml#L23) and replacements [here](https://github.com/openshift/cluster-storage-operator/blob/22b559adba3079be7276c020d2e8f982c83aae70/pkg/operator/csidriveroperator/csioperatorclient/aws.go#L19).

- AWS EBS operator example:
    ```shell
    export AWS_EBS_DRIVER_IMAGE=quay.io/openshift/origin-aws-ebs-csi-driver
    export AWS_EBS_DRIVER_OPERATOR_IMAGE=quay.io/openshift/origin-aws-ebs-csi-driver-operator
    ```

- Azure Disk and Azure File operator example:
    ```shell
    export AZURE_DISK_DRIVER_OPERATOR_IMAGE=quay.io/openshift/origin-azure-disk-csi-driver-operator:latest
    export AZURE_FILE_DRIVER_OPERATOR_IMAGE=quay.io/openshift/origin-azure-file-csi-driver-operator:latest
    export AZURE_DISK_DRIVER_IMAGE=quay.io/openshift/origin-azure-disk-csi-driver:latest
    export AZURE_FILE_DRIVER_IMAGE=quay.io/openshift/origin-azure-file-csi-driver:latest 
    ```

### Build and run CSO locally

```shell
# Build the operator (can be run before exporting variables)
make

# OPTIONAL - delete existing CSO lock
oc -n openshift-cluster-storage-operator delete lease/cluster-storage-operator-lock

# Run the operator via CLI
./cluster-storage-operator start --kubeconfig $KUBECONFIG --namespace openshift-cluster-storage-operator
```

## Volume path health recording rules

The rules in `assets/volumedeviceexporter/08_prometheusrule.yaml` keep metadata
normalization separate from device joins so their matching keys and deduplication
are explicit. At each evaluation:

- `csiaddons:volume_pv_info` emits one series per distinct
  `(persistentvolume, driver, volume_handle)` tuple with nonempty CSI metadata.
- `csiaddons:volume_bound_claim_info` emits at most one series per distinct
  nonempty volume name reported by Bound PVCs, including non-CSI volumes.
- `csiaddons:volume_pv_context_info` retains the CSI PV tuples, adding the selected
  bound claim labels when available.

Path alerts do not latch when an exporter mapping is
lost: once the mapping is absent from query results, its device recordings and
path alerts clear, even if the volume is unreachable. This can follow a stale
marker or expiry of the Prometheus lookback window, rather than happen
immediately on detachment. A returning mapping starts alert timers again.

## Prometheus rule tests

From the repository root, run:

```shell
make verify-prometheus-rules
```

The target installs `promtool` with
`go install -mod=mod github.com/prometheus/prometheus/cmd/promtool@v0.316.0` into
`_output/tools/bin/`. Go caches downloads and compilation results for subsequent
runs. Installation requires Go and network access; no container runtime is needed.
The target also ensures the repository's pinned `yq` v2 is available to extract
and update YAML using the existing build-machinery tool installer.
These tests also run as part of `make verify` (including the required CI verify
job) and `make check`. The tool installation overrides vendor mode only for
`promtool`; the operator's vendored dependencies are unchanged.

Alternatively, invoke the script directly with existing binaries without
installing them (`YQ` must point to the repository-compatible `yq` v2):

```shell
YQ=/absolute/path/to/yq PROMTOOL=/absolute/path/to/promtool ./hack/test-prometheus-rules.sh
```

For detailed evaluation output, prefix the command with `PROMTOOL_DEBUG=1`.
