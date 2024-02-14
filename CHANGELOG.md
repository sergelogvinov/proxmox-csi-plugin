## [0.5.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.4.1...v0.5.0) (2024-02-14)


### Features

* **chart:** add support to mount a custom CA ([9b94627](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/9b94627a9fb793b679b834efcf5ce84c36e2cd39))
* raw block device ([1be660b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/1be660bf3014fa3e1d40251561a67b76d1e9cf98))
* use readonly root ([ca00846](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/ca00846bb87c8f0e9a35c2ab7a6f2ceadb14bcf2))
* use release please tool ([39c4b22](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/39c4b223f64351c2c2e8685d55091ffe7d58a7ce))


### Bug Fixes

* release please ([bc02afd](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/bc02afdc7686cf7b35dd9d0e779065ee317c37bf))

## [v0.4.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.4.0...v0.4.1) (2024-01-01)

Welcome to the v0.4.1 release of Proxmox CSI Plugin!

### Bug Fixes

- publish shared volumes (a681b2b)
- find zone by region (4eae22d)

### Features

- **chart:** add value to customize kubeletDir (bbb627f)
- **chart:** add allowedTopologies (41cb02a)

### Miscellaneous

- release v0.4.1 (fd8d14f)
- bump deps (2a86bd7)
- bump deps (d8c98ea)
- bump deps (9054282)


## [v0.4.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.3.0...v0.4.0) (2023-10-24)

Welcome to the v0.4.0 release of Proxmox CSI Plugin!

### Bug Fixes

- check volume existence (aba0ca8)
- helm create namespace (364b8be)
- remove nocloud label (74e42b2)

### Features

- mkfs block/inode size options (88f4ebc)
- disk speed limit (c464dab)
- **chart:** make StorageClass parameters/mountOptions configurable (a78e338)

### Miscellaneous

- release v0.4.0 (764b741)
- bump deps (9e5a139)
- bump deps (a243ffb)


## [v0.3.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.2.0...v0.3.0) (2023-09-19)

Welcome to the v0.3.0 release of Proxmox CSI Plugin!

### Features

- storage encryption (26c1928)
- volume capability (1088dbb)
- regional block devices (c7d1541)

### Miscellaneous

- release v0.3.0 (324ad91)
- bump deps (5f5d781)
- bump actions/checkout from 3 to 4 (f75bfff)
- bump sigstore/cosign-installer from 3.1.1 to 3.1.2 (51419d3)
- bump deps (ae63a06)
- bump deps (4ceef77)


## [v0.2.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.1.1...v0.2.0) (2023-08-07)

Welcome to the v0.2.0 release of Proxmox CSI Plugin!

### Bug Fixes

- skip lxc containers on resize process (a24d24e)
- helm liveness context (e1ed889)
- detach volume error (dc128d1)
- kubectl apply in readme (bc2f88b)

### Features

- noatime flag for ssd (cd4f3f7)
- cosign images (5e13f3f)
- pin version (e81d8e3)
- helm oci release (c438712)
- drop node capabilities (927f664)
- trim filesystem (dc7dbbd)

### Miscellaneous

- release v0.2.0 (6a2d98a)
- bump actions versions (b477132)
- bump deps (f6d726c)
- bump deps (ecea2ad)
- bump deps (28f0a72)
- bump deps (f00f057)


## [v0.1.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.1.0...v0.1.1) (2023-05-12)

Welcome to the v0.1.1 release of Proxmox CSI Plugin!

### Features

- switch to distroless (ff1c9bf)
- decrease node image (93a04b6)

### Miscellaneous

- release v0.1.1 (429a420)
- bump deps (4e80caf)
- bump deps (be954c9)


## [v0.1.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.0.2...v0.1.0) (2023-05-04)

Welcome to the v0.1.0 release of Proxmox CSI Plugin!

### Bug Fixes

- release check (c3bd4e7)

### Miscellaneous

- release v0.1.0 (449bddf)


## [v0.0.2](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.01...v0.0.2) (2023-04-29)

Welcome to the v0.0.2 release of Proxmox CSI Plugin!

### Miscellaneous

- release v0.0.2 (8390a9f)


## v0.01 (2023-04-29)

Welcome to the v0.01 release of Proxmox CSI Plugin!

### Bug Fixes

- raise condition during volume attach (3bf3ef5)
- cluster schema (494a82b)

### Features

- resize pvc (bd2c653)
- node daemon (54dec7d)
- node daemonsets (269c708)
- controller (9f0f7a3)

### Miscellaneous

- release v0.0.1 (56b4297)
