# Standalone CSO permissions

The CVO supplies the nine CSI operator ClusterRoles, the vSphere problem detector
ClusterRole, the volume data source validator ClusterRole and the volume device
exporter auth proxy ClusterRole. It also supplies twelve component Roles: the
nine CSI operator Roles, the vSphere metrics Role, the AWS configuration Role
and the vSphere problem detector Role. CSO creates and reconciles their bindings.

The component Roles use `08_0005_*_role.yaml` filenames so the release builder
places them after `02_csi_driver_operators_namespace.yaml` and before
`10_deployment.yaml` within the CSO component. The previous `0008_05_` prefix
placed the Roles before the CSI namespace. The operator namespace is supplied
earlier by the global `0000_49_cluster-storage-operator_01_operator_namespace.yaml`
manifest. `openshift-config-managed`, required by the AWS configuration Role,
is a prerequisite supplied by another release component, as are `openshift-config`
and `kube-system` for CSO's own Roles.

The `cluster-storage-operator-main-clusterrole` contains CSO's cluster-scoped
permissions. A Role named `cluster-storage-operator` in each of the following
namespaces provides its namespaced permissions:

| Namespace | Operations |
| --- | --- |
| `openshift-cluster-storage-operator` | Reconcile component assets, Deployments, exporter DaemonSet, ConfigMaps and monitoring; read Secrets, Pods and ReplicaSets; record events; maintain the leader Lease; reconcile component bindings |
| `openshift-cluster-csi-drivers` | Reconcile CSI operator assets, Deployments, ConfigMaps, monitoring and bindings; read Secrets |
| `openshift-config` | Observe ConfigMaps and Secrets; delete the obsolete `selinux-conflicts` ConfigMap |
| `openshift-config-managed` | Observe ConfigMaps and Secrets; reconcile the AWS operator's configuration binding |
| `kube-system` | Observe ConfigMaps and Secrets; get `extension-apiserver-authentication` for delegated authentication |

The cluster-level permissions cover Storage configuration and status,
ClusterOperator lifecycle and status, ClusterCSIDriver creation and observation,
cluster configuration, StorageClass and VolumeAttributesClass metrics, CSI driver
detection, the volume populator CRD and the Manila namespace. The HTTPS server
also creates TokenReviews and SubjectAccessReviews.

The Role in `openshift-cluster-storage-operator` grants `use` on only the
`nonroot-v2` SecurityContextConstraints object. The existing RoleBinding grants
this permission to CSO's ServiceAccount within that namespace, allowing admission
of its Pod, whose Deployment explicitly requires `nonroot-v2`.

CSO does not update existing ClusterCSIDriver objects or their status, write
StorageClasses, or directly write Secrets. It does not create or modify
Roles or ClusterRoles. Creation grants are separate from named read/update grants
because RBAC cannot restrict `create` requests using `resourceNames`.

## Delegation and informer scope

`bind` on the twelve named component ClusterRoles allows CSO to assign them
without inheriting all their rules. Namespaced `bind` on the twelve named
component Roles allows CSO to assign those Roles. CSO has no `escalate` grant
and does not read, create or modify Roles. CVO owns their reconciliation,
including recreation after deletion. The Role in
`openshift-cluster-storage-operator` additionally allows binding
`system:openshift:scc:hostmount-anyuid-v2` within that namespace for the exporter.

These are delegation permissions: the restrictions on CSO's direct API calls do
not limit every permission it could acquire through the roles it can assign.
Their rules are controlled by the release payload rather than by CSO.

Named `escalate` grants are insufficient for Role creation: the collection POST
has no resource name in its URL. CVO ownership avoids granting CSO unnamed
escalation within the component namespaces.

Role informers are no longer registered by the standalone static resource
controllers because Roles are absent from their assets. Other informer scopes
are preserved:

- The monitoring factory lists and watches ServiceMonitors and PrometheusRules
  across all namespaces. Writes are limited to the two component namespaces.
- The resource sync controller registers ConfigMap and Secret informers in all
  five namespaces, including when only a ConfigMap is synchronized.
- Drivers' controllers register informers before platform selection. Permissions
  must cover these reads even on a cluster where the corresponding driver does
  not run.

Restricting these informers is a separate change. The nine CSI operator Role
definitions are also preserved without refinement.

## HyperShift

All new CSO grants and CVO-managed component Roles and ClusterRoles exclude the
HyperShift topology. HyperShift continues to reconcile the validator and exporter
ClusterRoles and CSI operator Roles from embedded assets. The shared kustomize
bases and HyperShift overlays are preserved. The problem detector is
standalone-only.

`manifests/08_operator_rbac.yaml` remains the input for the HyperShift profile,
but no longer includes standalone topologies. The standalone deletion manifest
removes its old `cluster-storage-operator-role` binding to `cluster-admin`.
Regenerating the HyperShift profile must leave its output unchanged.

## Local checks

Run from the repository root:

```bash
make update
make test-unit
make verify
```

The RBAC tests resolve the standalone manifest bindings for CSO's ServiceAccount,
check direct permission boundaries and compare delegation grants with the actual
component assets, including CVO ownership of the twelve Roles and the absence
of Role write or escalation grants. They also check component ClusterRole
ownership in both modes and the exporter's feature gate. They do not emulate
API-server admission or permissions granted by other bindings on a live cluster.
An ordering check compares release-builder filenames to ensure the local
namespace manifests precede Roles and the twelve component Roles precede the
standalone Deployment.

## Remote CI acceptance checklist

Run both installation and upgrade coverage using the candidate payload. An
existing installation's `cluster-admin` binding can mask missing permissions;
installing only the new roles is not a valid test of the change.

1. Confirm that `cluster-storage-operator-role` is absent in standalone and that
   no other ClusterRoleBinding or RoleBinding grants `cluster-admin` to CSO's
   ServiceAccount or its groups. Check for other elevated grants that could mask
   omissions as well.
2. Confirm the CSO Pod is created with `openshift.io/scc: nonroot-v2`, its
   Deployment becomes available and no new SCC admission rejections occur.
   Confirm `ClusterOperator/storage` becomes Available and settles without
   Progressing or Degraded. Check CSO logs for `Forbidden`, RBAC escalation
   failures and informer list/watch errors, including after a restart and leader
   election.
3. Confirm that CVO creates the twelve component ClusterRoles and twelve
   component Roles, while CSO creates the platform's operator ServiceAccount,
   bindings, Deployment and ClusterCSIDriver. Inspect the built payload's
   filenames and confirm the CSI namespace is created before its Roles, without
   `namespace not found` errors. On a fresh GCP installation,
   verify creation of `gcp-pd-csi-driver-operator-role` by CVO and successful
   startup of the GCP PD operator. Confirm the driver operator becomes healthy
   and `storage-obj-check` passes. Collect the deployed Roles and RoleBindings.
4. On a disposable CI cluster, delete a component Role and verify CVO recreates
   it. Verify that upgrades update existing Roles without CSO's old
   `cluster-admin` binding. Exercise CSO reconciliation of a RoleBinding,
   ClusterRoleBinding and ConfigMap, including recreating missing resources.
   This exercises binding admission, not just authorization to issue the API
   request.
5. Verify the validator Deployment, volume populator CRD, monitoring access and
   HTTPS metrics authentication/authorization. Inspect the leader Lease:

   ```bash
   oc get co storage
   oc get storage.operator.openshift.io cluster
   oc get leases.coordination.k8s.io cluster-storage-operator-lock \
     -n openshift-cluster-storage-operator
   oc logs deployment/cluster-storage-operator \
     -n openshift-cluster-storage-operator
   ```

6. On vSphere, verify the problem detector, credential and serving-certificate
   observation, monitoring assets and alert enable/disable reconciliation.
7. On OpenStack, verify Cinder and Manila, creation of the Manila namespace and
   synchronization of `openshift-config/cloud-provider-config` into the CSI
   operator namespace. On AWS, verify the Role and binding in
   `openshift-config-managed`.
8. With `StoragePathAlert` enabled, verify exporter resources, the hostmount-anyuid-v2 SCC
   RoleBinding, CA synchronization from `kube-system`, DaemonSet and monitoring.
   Confirm the exporter is not started when the gate is disabled.

Record which platforms and gates the CI actually exercised. Passing one cloud's
tests does not establish coverage for the platform-specific cases above.
Check any new RBAC denials against the exact revision tested; denials from an
earlier revision do not establish missing permissions in the candidate payload.
