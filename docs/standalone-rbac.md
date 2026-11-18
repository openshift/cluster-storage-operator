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

Phase 1 moves ownership of guest component RBAC to the release payload. The
CVO supplies nine guest ClusterRoles: the seven supported CSI operators (AWS,
Azure Disk, Azure File, GCP PD, Cinder, Manila and PowerVS), the validator and the
exporter auth proxy. Their rules are identical to the standalone definitions.
IBM VPC Block, vSphere and the vSphere problem detector remain standalone-only.

The CVO also supplies six guest Roles in `openshift-cluster-csi-drivers`.
PowerVS shares its standalone Role. AWS, Azure Disk, Azure File, GCP PD and
Cinder use variants generated from the standalone manifests by
`profile-patches/hypershift-guest/`. These variants carry only the HyperShift
inclusion and Storage capability annotations and preserve the guest operators'
additional PVC permissions. Standalone rules are unchanged. Manila has no guest
Role or RoleBinding, matching its previous behavior. The `08_0005_` filenames
place these Roles after the CSI namespace in the payload.

CSO reconciles guest ServiceAccounts and bindings, but no longer applies guest
Roles or ClusterRoles, including those for the validator and exporter. Shared
kustomize bases remain available to the management overlays. Management Roles,
the dynamic `${CONTROLPLANE_NAMESPACE}`, management bindings and guest
credentials are unchanged. The exporter controllers still run only when
`StoragePathAlert` is enabled; its CVO-managed ClusterRole alone does not start
an exporter workload.

Phase 2 will reduce CSO's effective HyperShift privileges. Phase 1 keeps the
legacy `cluster-storage-operator-role` binding to `cluster-admin` and all
artifacts copied by the control-plane-operator. The current HyperShift Deployment
mounts `service-network-admin-kubeconfig`: moving component RBAC to CVO does not
reduce the privileges of that administrative guest identity. The standalone CSO
grants remain excluded from HyperShift, and no cross-repository or credential
changes are part of this phase.

`manifests/08_operator_rbac.yaml` remains the input for the HyperShift profile.
Its generated `08_operator_rbac-hypershift.yaml` output stays unchanged. The
standalone deletion manifest removes the legacy binding only in standalone
topologies. Common status controllers continue to report that legacy binding in
HyperShift `relatedObjects` and the dedicated binding in standalone.

## Local checks

Run from the repository root:

```bash
make update
make check
```

The RBAC tests resolve the standalone manifest bindings for CSO's ServiceAccount,
check direct permission boundaries and compare delegation grants with the actual
component assets, including CVO ownership of the twelve Roles and the absence
of Role write or escalation grants. Strict YAML conversion rejects duplicate
keys. Tests also check uniqueness of Roles and ClusterRoles in each topology,
guest rules against standalone rules plus the historical PVC grants, absence
of guest role assets, guest binding
references, management Role ownership and dynamic namespaces, and the exporter's
feature gate. They do not emulate API-server admission or permissions granted
by other bindings on a live cluster.
Ordering checks compare release-builder filenames in all four topologies to
ensure the local namespace manifests precede Roles; standalone component Roles
must also precede the standalone Deployment.

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

## HyperShift remote CI acceptance (phase 1)

Use a candidate payload in remote HyperShift CI for installation and upgrade
coverage. Do not infer runtime success from the local manifest and unit checks.

1. Confirm CVO creates exactly the nine guest ClusterRoles and six guest Roles
   above. Confirm the CSI namespace precedes its Roles in the built payload and
   no `NotFound` errors occur during installation or upgrade.
2. Confirm CSO reconciles the guest bindings and ServiceAccounts and that CSI
   operator management Roles, bindings and Deployments remain healthy in their
   tenant control plane namespace. Recreate a guest binding on a disposable CI
   cluster and verify reconciliation; recreate a guest Role and verify CVO
   restores it.
3. Confirm `ClusterOperator/storage` is Available without Degraded, the platform
   CSI operator becomes available, the default StorageClass exists, and storage
   provisioning and `storage-obj-check` pass. Inspect CSO and driver operator
   logs for `NotFound`, `Forbidden` and informer errors after restart as well.
4. Confirm validator functionality and, with `StoragePathAlert` enabled, exporter
   reconciliation, SCC binding, DaemonSet and monitoring. With the gate disabled,
   confirm exporter controllers do not start.
5. Record the actual guest identity, its groups and the kubeconfig source used
   by CSO (for example, `oc --kubeconfig=<guest-kubeconfig> whoami -o json`, run
   in the CI environment without publishing credentials). Record the retained
   administrative bindings and the tested payload revision as input to phase 2.
   A successful run with an administrative kubeconfig validates phase 1
   integration; it does not validate minimum CSO RBAC.

Record CI links, platform, feature gates and installation/upgrade outcomes in
the PR. These remote checks require a candidate payload and are not performed
by `make check`.
