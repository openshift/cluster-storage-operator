# Agent Guide: cluster-storage-operator

This file provides guidance to AI agents when working with code in this repository.

The cluster-storage-operator (CSO) is an OpenShift cluster operator that manages cluster-wide storage defaults. It:

- Deploys and lifecycle-manages per-platform CSI driver operators (AWS EBS, Azure Disk, Azure File, GCP PD, IBM VPC Block, OpenStack Cinder, OpenStack Manila, PowerVS Block, vSphere)
- Ensures a default `StorageClass` exists for the cluster platform
- Runs the vSphere Problem Detector on vSphere clusters
- Reports operator status via the `storage` `ClusterOperator` object
- Enforces storage-related admission policies (namespace label validation via `ValidatingAdmissionPolicy`)

CSO does **not** implement CSI drivers directly. It installs and manages the operator for each CSI driver (e.g., `aws-ebs-csi-driver-operator`), which in turn manages the driver itself.

---

## Repository Layout

```
cluster-storage-operator/
├── assets/                          # Embedded static manifests (YAML)
│   ├── csidriveroperators/          # Per-driver kustomize bases + generated output
│   │   ├── aws-ebs/
│   │   │   ├── base/                # Shared kustomize resources (SA, RBAC, CR, Deployment)
│   │   │   ├── standalone/          # Standalone (non-HyperShift) overlay + patches
│   │   │   │   └── generated/       # Output of `make update` — DO NOT EDIT MANUALLY
│   │   │   └── hypershift/
│   │   │       ├── guest/           # Resources applied to the guest cluster
│   │   │       │   └── generated/   # Output of `make update` — DO NOT EDIT MANUALLY
│   │   │       └── mgmt/            # Resources applied to the management cluster
│   │   │           └── generated/   # Output of `make update` — DO NOT EDIT MANUALLY
│   │   ├── azure-disk/
│   │   ├── azure-file/
│   │   ├── gcp-pd/                  # No hypershift support (standalone only, no generated/)
│   │   ├── ibm-vpc-block/           # No hypershift support
│   │   ├── powervs-block/
│   │   └── vsphere/                 # No hypershift support
│   ├── vsphere_problem_detector/    # Static assets for the vSphere problem detector
│   └── volumedatasourcevalidator/   # Static assets for the volume data source validator
├── manifests/                       # Cluster-level manifests (CVO-managed, applied at install time)
│   ├── image-references             # All container images managed by this operator
│   ├── 03_credentials_request_*.yaml # Cloud IAM permissions per CSI driver (see CredentialsRequest Changes)
│   ├── 06_operator_cr.yaml          # Storage CR — main CSO configuration (operator.openshift.io/v1 Storage)
│   ├── 08_operator_rbac.yaml        # CSO's own ClusterRoleBinding
│   ├── 09_sidecar-*.yaml            # Shared RBAC for CSI sidecars (provisioner, attacher, etc.)
│   ├── 10_deployment.yaml           # CSO Deployment
│   └── 11_cluster_operator.yaml     # ClusterOperator status object for the storage operator
├── hack/
│   ├── generate-manifests.sh        # Runs `oc kustomize` for all drivers → populates generated/
│   └── verify-manifest.sh           # CI check: fails if generated/ is out of date
├── pkg/
│   ├── csoclients/                  # Kubernetes/OpenShift client wrappers (standalone + HyperShift)
│   └── operator/
│       ├── starter.go               # Entry point: RunOperator, selects standalone vs HyperShift
│       ├── operator_starter.go      # StandaloneStarter + HyperShiftStarter implementations
│       ├── csidriveroperator/
│       │   ├── csioperatorclient/   # Per-driver CSIOperatorConfig (aws.go, azure-disk.go, …)
│       │   ├── driver_starter.go    # Controller that starts per-platform driver managers
│       │   ├── deploymentcontroller.go
│       │   ├── hypershift_deployment_controller.go
│       │   └── crcontroller.go      # Reconciles ClusterCSIDriver CR log level / status
│       ├── defaultstorageclass/     # Ensures a default StorageClass exists
│       ├── vsphereproblemdetector/  # vSphere problem detector lifecycle
│       ├── volumedatasourcevalidator/
│       ├── metrics/                 # StorageClass and VolumeAttributesClass metrics
│       └── configobservation/       # Observes cluster config (proxy, etc.)
└── cmd/                             # Binary entry point
```

---

## Key Architecture Components

### Two Deployment Modes

**Standalone** (self-managed OCP): CSO runs on the cluster it manages. The CSI driver operator Deployment and all guest resources live in `openshift-cluster-csi-drivers`.

**HyperShift**: CSO runs on a management cluster and manages a separate guest cluster. The CSI driver operator **Deployment** runs in the management cluster (in a per-tenant control plane namespace), while guest resources (ServiceAccount, RBAC, ClusterCSIDriver CR) are applied to the guest cluster. Asset paths are split — `hypershift/mgmt/` for management cluster resources, `hypershift/guest/` for guest cluster resources.

Mode is selected in `pkg/operator/starter.go` based on whether `--guest-kubeconfig` is provided.

### CSIOperatorConfig

Each CSI driver is described by a `CSIOperatorConfig` struct (`pkg/operator/csidriveroperator/csioperatorclient/types.go`). Key fields:

| Field | Purpose |
| --- | --- |
| `CSIDriverName` | CSI driver name (e.g., `ebs.csi.aws.com`) and name of the ClusterCSIDriver CR |
| `Platform` | Platform where this driver runs, or `AllPlatforms` |
| `StatusFilter` | Optional callback for sub-platform filtering (e.g., Azure Stack Hub) |
| `StaticAssets` | YAML assets applied to guest/standalone cluster |
| `MgmtStaticAssets` | YAML assets applied to management cluster (HyperShift only) |
| `DeploymentAsset` | Path to the driver operator Deployment asset |
| `CRAsset` | Path to the ClusterCSIDriver CR asset |
| `ImageReplacer` | Replaces `${OPERATOR_IMAGE}`, `${DRIVER_IMAGE}`, etc. with env var values |
| `AllowDisabled` | If true, driver absence on unsupported sub-platforms does not degrade CSO |
| `RequireFeatureGate` | If set, driver is Tech Preview and only starts when the feature gate is enabled |

Each driver's config lives in `pkg/operator/csidriveroperator/csioperatorclient/<driver>.go`.

### Asset Generation

Assets under `generated/` are produced by `oc kustomize` and **must never be edited by hand**.

- Sources: `assets/csidriveroperators/<driver>/base/` composed with `standalone/` or `hypershift/guest/` and `hypershift/mgmt/` overlays
- Drivers managed by the generator: `aws-ebs`, `azure-disk`, `azure-file`, `openstack-cinder`, `openstack-manila`
- Drivers with static (non-generated) assets: `gcp-pd`, `ibm-vpc-block`, `powervs-block`, `vsphere`

Edit source files in `base/`, `standalone/`, or `hypershift/` overlays; run `make update` before committing. CI enforces freshness via `hack/verify-manifest.sh`.

### Image References

`manifests/image-references` is an `ImageStream` that declares every container image managed by this operator. OpenShift's ART build system uses it to pin image digests in release payloads. Every new container image (new sidecar, new driver variant) must be listed here.

### Manifest Topology Annotations

Manifests under `manifests/` use annotations to control which cluster topology includes them:

```yaml
include.release.openshift.io/hypershift: "true"
include.release.openshift.io/ibm-cloud-managed: "true"
include.release.openshift.io/self-managed-high-availability: "true"
include.release.openshift.io/single-node-developer: "true"
capability.openshift.io/name: Storage
```

A manifest missing these annotations will be silently excluded from some topologies.

---

## Common Development Commands

| Command | Description |
| --- | --- |
| `make` | Build the operator binary |
| `make update` | Regenerate all assets including kustomize `generated/` dirs (requires `oc` on `$PATH`) |
| `make test-unit` | Run unit tests |
| `make verify` | Run all static checks |
| `make check` | `verify` + `test-unit` |
| `hack/verify-manifest.sh` | Check that `generated/` assets are up to date (same check CI runs) |

---

## PR Review

### Critical Review Process

**Follow this order for every PR:**

1. **Generated assets up to date.** If `base/`, `standalone/`, or `hypershift/` source files changed, check CI or run `hack/verify-manifest.sh` to confirm `generated/` was regenerated.
2. **Standalone and HyperShift symmetry.** Driver RBAC/Deployment/CR changes almost always need both paths updated — check `StandaloneStarter.populateConfigs()` and `HyperShiftStarter.populateConfigs()` in `operator_starter.go`.
3. **`image-references` complete.** Every new `${...}_IMAGE` placeholder in a Deployment asset must have a matching entry in `manifests/image-references`.
4. **Manifest topology annotations correct.** New manifests in `manifests/` must carry the appropriate `include.release.openshift.io/*` annotations or they will be silently excluded from some topologies.
5. **RBAC least-privilege.** New or modified RBAC must use the minimum required permissions with correct subjects and namespaces.
6. **Feature gate status.** New Tech Preview drivers must set `RequireFeatureGate`; GA drivers must not have it set.
7. **`CredentialsRequest` changes.** If any `manifests/03_credentials_request_*.yaml` is modified, flag it to the PR author — cross-repo coordination is required (see below).

### Failure Conditions

A PR review **must fail** if any of the following are true:

- `generated/` assets are out of date relative to their kustomize source overlays
- A new container image is used in an asset but absent from `manifests/image-references`
- A new manifest in `manifests/` is missing `include.release.openshift.io/*` annotations
- A new Tech Preview driver does not set `RequireFeatureGate`
- A GA driver has `RequireFeatureGate` still set
- RBAC rules grant permissions broader than what the component demonstrably needs
- A `ValidatingAdmissionPolicy` change uses `failurePolicy: Fail` without consideration of upgrade safety
- A new driver is registered in `StandaloneStarter.populateConfigs()` but not evaluated for HyperShift support (or vice versa — it must be a deliberate decision, documented in the PR)
- Any `manifests/03_credentials_request_*.yaml` file is modified without the PR description documenting the required cross-repo coordination (see below)

### RBAC Changes

- RBAC files under `assets/csidriveroperators/<driver>/base/` govern the CSI driver *operator*, not the driver itself. Verify the subject, namespace, and rules match the minimum needed.
- CSO itself currently runs with `cluster-admin` (`manifests/08_operator_rbac.yaml`) — this is a known open TODO, not a new concern introduced by a PR.
- Sidecar RBAC for provisioner / attacher / resizer / snapshotter lives in `manifests/09_sidecar-*.yaml` and is shared across all drivers. Changes there affect every driver simultaneously.

### Adding a New CSI Driver

Verify the PR includes all of the following:

1. `assets/csidriveroperators/<driver>/` with `base/`, `standalone/`, and optionally `hypershift/guest/` + `hypershift/mgmt/`
2. A `GetXxxCSIOperatorConfig()` function in `pkg/operator/csidriveroperator/csioperatorclient/<driver>.go`
3. Registration in `StandaloneStarter.populateConfigs()` in `pkg/operator/operator_starter.go`
4. Registration in `HyperShiftStarter.populateConfigs()` if HyperShift is supported
5. Driver and operator images added to `manifests/image-references`
6. Driver added to the `drivers` array in `hack/generate-manifests.sh` if it uses kustomize generation
7. A `CredentialsRequest` at `manifests/03_credentials_request_<driver>.yaml` if cloud credentials are required
8. All new drivers should start as Tech Preview with `RequireFeatureGate` set

### Platform Filtering Logic

`shouldRunController()` in `pkg/operator/csidriveroperator/driver_starter.go` starts a driver only when: the cluster platform matches `cfg.Platform` (or `AllPlatforms`), `cfg.StatusFilter` returns true (if set), `cfg.RequireFeatureGate` is enabled (if set), and no third-party CSI driver with the same name is installed without the `csi.openshift.io/managed` annotation (which would degrade the cluster).

### Default StorageClass Controller

`pkg/operator/defaultstorageclass/controller.go` — all major cloud platforms currently return `supportedByCSIError`, meaning the default StorageClass is the CSI driver operator's responsibility. Only add a new `case` here if a platform's default StorageClass is genuinely owned by CSO and not by a CSI driver operator.

### CredentialsRequest Changes

`manifests/03_credentials_request_*.yaml` files define the cloud IAM permissions granted to each CSI driver via OpenShift's [Cloud Credential Operator (CCO)](https://github.com/openshift/cloud-credential-operator). Each file maps to one driver:

**Any modification to these files requires cross-repo coordination.** When you detect a change to any `03_credentials_request_*.yaml`, post the following comment on the PR:

> **Action required — CredentialsRequest change detected.**
>
> Modifying a `CredentialsRequest` changes the cloud IAM permissions granted to a CSI driver. This affects multiple projects and must be coordinated:
>
> 1. **CSI driver operator repo** — the driver operator (e.g., `openshift/aws-ebs-csi-driver-operator`) may maintain its own copy of the `CredentialsRequest` or rely on the one shipped here. Confirm which is authoritative and whether the operator repo needs a matching update.
> 2. **AWS-specific: STS / manual-mode IAM policies** — for AWS, clusters running in [STS mode](https://docs.openshift.com/container-platform/latest/authentication/managing_cloud_provider_credentials/cco-mode-sts.html) or manual-mode CCO require IAM policies to be updated in the installer or in customer-managed policy documents. Adding a new `ec2:*` or `kms:*` action here is not enough on its own for those clusters.
> 3. **Cloud Credential Operator** — if a new `ProviderSpec` field or a new provider kind is used, CCO may need to be updated first to understand and process it.
> 4. **Release notes / documentation** — new IAM permissions are a customer-visible change and should be noted in the release notes, especially for manual-mode customers who manage their own policies.
>
> Please add a note to this PR description listing which of the above have been addressed and link to any related PRs.

**What to check in the diff itself:**

- For AWS (`AWSProviderSpec`): each added `action` grants that EC2/KMS API call to the driver. Confirm the new action is actually called by the driver code and is the minimum scope needed. The `resource: "*"` is standard but note it.
- For Azure (`AzureProviderSpec`): added `permissions` follow the `Microsoft.<service>/<resource>/<action>` pattern. Confirm they match what the driver requires.
- For GCP (`GCPProviderSpec`): `predefinedRoles` grant broad role bundles; prefer fine-grained `permissions` entries where possible.
- Removed permissions: confirm the driver no longer calls the corresponding API. Removing a permission that the driver still uses will cause runtime failures.

### Admission Policy Changes

`manifests/13_validating_admission_policy.yaml` uses `failurePolicy: Fail`. Any CEL expression change must be reviewed for:
- Correctness of the CEL logic (the policy validates `storage.openshift.io/fsgroup-change-policy` and `storage.openshift.io/selinux-change-policy` namespace labels)
- Impact on cluster upgrade paths — a broken policy with `failurePolicy: Fail` can block namespace creation cluster-wide

---

## Common Patterns

### Image Substitution

Deployment assets contain placeholders (`${DRIVER_IMAGE}`, `${OPERATOR_IMAGE}`, `${PROVISIONER_IMAGE}`, etc.). At runtime CSO replaces them using `strings.NewReplacer` built from environment variables. The env var names are constants in each `csioperatorclient/<driver>.go`. Any new placeholder added to a Deployment asset requires:
1. A matching constant and `os.Getenv()` call in the driver's config file
2. An entry in `manifests/image-references`
3. Documentation in `README.md` under the provider-specific env vars section

### Condition Naming

Operator conditions follow the pattern `<ConditionPrefix><ConditionType>` (e.g., `AWSEBSCSIDriverOperatorDeploymentAvailable`). The prefix comes from `CSIOperatorConfig.ConditionPrefix`. Stale conditions from removed controllers are cleaned up by `staleconditions.NewRemoveStaleConditionsController` in `operator_starter.go`.

### HyperShift Namespace Substitution

Management cluster assets use the literal placeholder `${CONTROLPLANE_NAMESPACE}`. At runtime `namespaceReplacer()` in `driver_starter.go` substitutes the actual control plane namespace. Any new management cluster asset that references a namespace must use this placeholder.

---

## Running Locally

See `README.md` for full setup. In brief:

```bash
oc scale --replicas=0 deploy/cluster-version-operator -n openshift-cluster-version
oc scale --replicas=0 deploy/cluster-storage-operator -n openshift-cluster-storage-operator
# export image env vars per README.md, then:
make
./cluster-storage-operator start --kubeconfig $KUBECONFIG --namespace openshift-cluster-storage-operator
```
