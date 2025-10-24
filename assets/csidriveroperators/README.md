# `csidriveroperators` assets

This directory contains assets used to deploy the various CSI Driver Operators.
Each subdirectory contains assets for a given CSI Driver Operator.
Historically, the actual CSI Driver Operators have lived in their own
repositories, but starting in 4.16 they have been migrating to the
`openshift/csi-operator` repository.

If the driver has added support for Hypershift deployments, you will generally
find the following directory structure.

```
assets/csidriveroperators/{driver}/
├── base
│   └── kustomization.yaml
├── hypershift
│   ├── guest
│   │   ├── generated
│   │   └── kustomization.yaml
│   └── mgmt
│       ├── generated
│       └── kustomization.yaml
└── standalone
    ├── generated
    └── kustomization.yaml
```

The `base` directory provides the base assets for the driver. The `standalone`
directory contains both patches for these base assets and additional assets,
handling things that only apply in a standalone deployment. Likewise, the
`hypershift/guest` and `hypershift/mgmt` directories contain assets and asset
patches that only apply in the guest and management clusters of a
[Hypershift/HCP][hcp] deployment, respectively. Finally, in each directory
there is `kustomization.yaml` file describing how the patches and assets are
combined to generate the resulting assets, which are dumped to the `generated`
subdirectory in the `standalone`, `hypershift/guest` and `hypershift/mgmt`
directories.

[hcp]: https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html-single/hosted_control_planes/index
