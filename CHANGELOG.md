## [0.10.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.9.0...v0.10.0) (2025-01-20)


### Features

* enable support for capmox ([6145c7d](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/6145c7d91cfc47c131ac453e2a90a915e5694b2b))

## [0.15.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.14.0...v0.15.0) (2025-11-01)


### Features

* support vm name with fqdn ([1cf812b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/1cf812b09e659ee22c69d5946f79e86e5c90c314))
* update distroless debian image ([f50da79](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/f50da79a6e96707cccc7c31e879f2fb644eec777))


### Bug Fixes

* backup volume attibute ([62aba00](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/62aba00732e8c91595d85b1c425ca51442af4715))

## [0.14.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.13.0...v0.14.0) (2025-10-05)


### Features

* add config options token_id_file & token_secret_file ([64dc356](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/64dc356ec37085ab8d5de4d136537e08e94c3d49))


### Bug Fixes

* **chart:** ssd parameter ([dc07d1b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/dc07d1b1884778b5614a6c853d1e8c87d6d5cb3c))
* pveum role add command missing '.Disk' in install.md ([ea55e23](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/ea55e23f07496626779de896deab0c1303f47c79))

## [0.13.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.12.1...v0.13.0) (2025-07-20)


### Features

* **chart:** customize pod labels ([93c1175](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/93c11750ac952ce006ebe50758e95f4e1f85c61b))
* concurrent attach/detach pv ([3db4ca4](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/3db4ca40b8a301bb8c0426db34c23d12939f24bf))
* get capacity with shared storages ([fa9c34b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/fa9c34bc0803236e793a7ce60fcc2c15af94ec05))
* qcow2 storage format ([8576e3c](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/8576e3ca1405826255dd7af48a418f19b4a70742))


### Bug Fixes

* concurrent attach/detach pv ([af365ca](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/af365cadd632689b2f1b0bfaaf1a024c6cf1239c))
* get volume annotation ([5528f1d](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/5528f1d0706196e3a57a3c73b0f9ea35ceb4cc80))

## [0.12.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.12.0...v0.12.1) (2025-06-10)


### Bug Fixes

* unpublish when the node no longer exists ([a5d8c70](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/a5d8c70240602788baf93c294bd1366c0fc06486))

## [0.12.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.11.0...v0.12.0) (2025-06-09)


### Features

* **chart:** add CSIStorageCapacity as option ([2a0f8c7](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/2a0f8c75fde0fd3cf421dbba7167f0ca46f505b7))
* configurable maximum volume attachments per node ([0a4a98f](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/0a4a98ffedef116d91d20cf60a8b9bb02e30e490))
* enforce single commit requirement ([f7b6ff1](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/f7b6ff1bb570e28dc3b26521507748025fde0016))
* lifecycle management for pv ([0f7cd72](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/0f7cd7237029ab20ef94e44b43c457785c834614))


### Bug Fixes

* **chart:** add missing volumeattributesclasses rule to ClusterRole ([1f10218](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/1f10218dce4c78191d390b5fa839c6cd2516e33e))
* empty named vms ([2da5ee4](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/2da5ee4c2450cbc00c7a38bb032ac7ee6fd74ded))
* remove the pv from the dead node ([b0bcd03](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/b0bcd037611e1a97119d5593baed41156fc64218))

## [0.11.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.10.0...v0.11.0) (2025-02-08)


### Features

* allow ovverid backup attribute ([2fada12](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/2fada12f0a0305a7083debff4c94b088b721cf04))
* support different disk id ([e3a25c2](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/e3a25c26a2152d8605fef42e8b0c7e2b3b3c26c4))
* support volume attributes class ([bab93fb](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/bab93fb05355f4e65995b60a7ec003b129fbe984))
* volume replication ([0b66712](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/0b667121a527b01652a773f3274af7f65dc7b7f6))
* zfs storage migration ([37d7fb0](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/37d7fb09f2e76fa4ea5b40377777a19e8832f09e))


### Bug Fixes

* parametes attributes ([820cb7e](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/820cb7ea11e09d1d8c7c6feff176350b20135f62))

## [v0.9.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.8.2...v0.9.0) (2025-01-01)

Welcome to the v0.9.0 release of Proxmox CSI Plugin!

### Bug Fixes

- volume size (b08a592)

### Features

- minimal chunk size (898f6e7)

### Miscellaneous

- release v0.9.0 (1555d55)
- bump deps (a30235b)
- bump deps (db61132)
- bump deps (0695c22)
- bump deps (2351ca2)
- release v0.8.2 (0cd72b0)
- **chart:** update csi sidecar (d3b2b84)


## [v0.8.2](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.8.1...v0.8.2) (2024-09-28)

Welcome to the v0.8.2 release of Proxmox CSI Plugin!

### Bug Fixes

- log sanitizer (474e734)

### Miscellaneous

- release v0.8.2 (0274c03)


## [v0.8.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.8.0...v0.8.1) (2024-09-24)

Welcome to the v0.8.1 release of Proxmox CSI Plugin!

### Bug Fixes

- release please (593f605)
- goreleaser (4e0e87a)

### Miscellaneous

- release v0.8.1 (3f8bd85)


## [v0.8.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.7.0...v0.8.0) (2024-09-23)

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

- release v0.8.0 (589de9c)
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
