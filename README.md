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
