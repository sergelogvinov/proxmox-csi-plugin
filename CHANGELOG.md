## [0.8.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.7.0...v0.8.0) (2024-09-23)


### Features

* add unsafe env ([36fa532](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/36fa5324074d6a695404c0c94fee65ff35c2d96e))
* expose metrics ([4bbe65d](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/4bbe65dccc54192005d663fef86c2c40fd1c3b2c))


### Bug Fixes

* allow nfs shared storages ([04cfb97](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/04cfb97993fb536994a8cf0da6542ea8f6fd696c))
* check rbac permission ([57a6b0d](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/57a6b0dbb7b60309f9185a475ac1e949878ff349))
* helm chart metrics option ([e5ef1b1](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/e5ef1b132251e7bfb98234ee3ea935524db55d16))
* helm chart podAnnotation ([b935d88](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/b935d88e14df3982ff59541ee0732e5b421a2088))

## [v0.8.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.7.0...v0.8.0) (2024-09-20)

Welcome to the v0.8.0 release of Proxmox CSI Plugin!

### Bug Fixes

- check rbac permission (57a6b0d)
- helm chart metrics option (e5ef1b1)
- allow nfs shared storages (04cfb97)
- helm chart podAnnotation (b935d88)

### Features

- expose metrics (4bbe65d)
- add unsafe env (36fa532)

### Miscellaneous

- bump deps (9a0161b)
- bump deps (3c3c122)
- bump deps (c5769c1)
- **chart:** update readme (c76555a)


## [v0.7.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.6.1...v0.7.0) (2024-06-14)

Welcome to the v0.7.0 release of Proxmox CSI Plugin!

### Bug Fixes

- implement structured logging (cb5fb4e)
- pv force migration (8ecf990)

### Features

- wait volume to be detached (3683d96)
- swap pv in already created pvc (76c899e)

### Miscellaneous

- release v0.7.0 (9424c06)
- release v0.7.0 (7362940)
- bump deps (5bf0677)
- bump deps (89adec9)
- release v0.6.1 (ac1ef92)


## [v0.6.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.6.0...v0.6.1) (2024-04-13)

Welcome to the v0.6.1 release of Proxmox CSI Plugin!

### Bug Fixes

- build release (facdec5)
- release doc (215c366)

### Miscellaneous

- release v0.6.1 (e7dfde2)


## [v0.6.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.5.0...v0.6.0) (2024-04-13)

Welcome to the v0.6.0 release of Proxmox CSI Plugin!

### Bug Fixes

- pvc migration (ddfc362)
- deps update (657ad00)
- cli migration (41b19bd)
- goreleaser (04a40f4)

### Features

- remove udev dependency (1810ec7)
- **chart:** support setting annotations and labels on storageClasses (a5f5add)
- **chart:** add initContainers and hostAliases (769c008)

### Miscellaneous

- release v0.6.0 (0b13bd0)
- bump deps (67dc34c)
- bump deps (2f9f17a)
- **chart:** update sidecar deps (5f16e6b)


## [v0.5.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.4.1...v0.5.0) (2024-02-20)

Welcome to the v0.5.0 release of Proxmox CSI Plugin!

### Bug Fixes

- add delay before unattach device (ff575d1)
- release please (ffad744)
- **chart:** detect safe mounted behavior (5580695)

### Features

- prefer providerID (7dcde72)
- pv/pvc cli helper (d97bc32)
- use release please tool (39c4b22)
- use readonly root (ca00846)
- raw block device (1be660b)
- **chart:** add support to mount a custom CA (9b94627)

### Miscellaneous

- release v0.5.0 (a361ce9)
- bump deps (ac4ddd0)


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
