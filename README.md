# cluster-storage-operator
Operator that sets OCP cluster wide storage defaults.

Ensures a default storage class exists for OCP clusters, like the [addon-manager](https://github.com/kubernetes/kubernetes/tree/release-1.13/cluster/addons/storage-class) does for kubernetes clusters. Supports AWS and OpenStack. No configuration is required. The created storage class can be made non-default by editing its annotation but cannot be deleted so long as the operator runs.

Will also ensure default CSI volume plugins are installed in a future release when CSI plugins replace in-tree ones (see [csi-operator](https://github.com/openshift/csi-operator)).

blabla