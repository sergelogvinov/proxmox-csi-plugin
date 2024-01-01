
<a name="v0.4.1"></a>
## [v0.4.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.4.0...v0.4.1) (2024-01-01)

Welcome to the v0.4.1 release of Proxmox CSI Plugin!

### Bug Fixes

- publish shared volumes
- find zone by region

### Features

- **chart:** add value to customize kubeletDir
- **chart:** add allowedTopologies

### Changelog

* a681b2b fix: publish shared volumes
* 2a86bd7 chore: bump deps
* bbb627f feat(chart): add value to customize kubeletDir
* 4eae22d fix: find zone by region
* d8c98ea chore: bump deps
* 41cb02a feat(chart): add allowedTopologies
* 4c9bae4 ci: update bug template
* 9054282 chore: bump deps

<a name="v0.4.0"></a>
## [v0.4.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.3.0...v0.4.0) (2023-10-24)

Welcome to the v0.4.0 release of Proxmox CSI Plugin!

### Bug Fixes

- check volume existence
- helm create namespace
- remove nocloud label

### Features

- mkfs block/inode size options
- disk speed limit
- **chart:** make StorageClass parameters/mountOptions configurable

### Changelog

* 764b741 chore: release v0.4.0
* 9e5a139 chore: bump deps
* aba0ca8 fix: check volume existence
* a78e338 feat(chart): make StorageClass parameters/mountOptions configurable
* 364b8be fix: helm create namespace
* 74e42b2 fix: remove nocloud label
* 88f4ebc feat: mkfs block/inode size options
* c464dab feat: disk speed limit
* a243ffb chore: bump deps

<a name="v0.3.0"></a>
## [v0.3.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.2.0...v0.3.0) (2023-09-19)

Welcome to the v0.3.0 release of Proxmox CSI Plugin!

### Features

- storage encryption
- volume capability
- regional block devices

### Changelog

* 324ad91 chore: release v0.3.0
* 5f5d781 chore: bump deps
* 5e57204 docs: storage class options
* 26c1928 feat: storage encryption
* f75bfff chore: bump actions/checkout from 3 to 4
* 1088dbb feat: volume capability
* 51419d3 chore: bump sigstore/cosign-installer from 3.1.1 to 3.1.2
* ae63a06 chore: bump deps
* c7d1541 feat: regional block devices
* 4ceef77 chore: bump deps

<a name="v0.2.0"></a>
## [v0.2.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.1.1...v0.2.0) (2023-08-07)

Welcome to the v0.2.0 release of Proxmox CSI Plugin!

### Bug Fixes

- skip lxc containers on resize process
- helm liveness context
- detach volume error
- kubectl apply in readme

### Features

- noatime flag for ssd
- cosign images
- pin version
- helm oci release
- drop node capabilities
- trim filesystem

### Changelog

* 6a2d98a chore: release v0.2.0
* a24d24e fix: skip lxc containers on resize process
* cd4f3f7 feat: noatime flag for ssd
* b477132 chore: bump actions versions
* 5e13f3f feat: cosign images
* e81d8e3 feat: pin version
* f6d726c chore: bump deps
* c438712 feat: helm oci release
* 927f664 feat: drop node capabilities
* dc7dbbd feat: trim filesystem
* e1ed889 fix: helm liveness context
* d7e0bec ci: build timeout
* ecea2ad chore: bump deps
* 28f0a72 chore: bump deps
* dc128d1 fix: detach volume error
* bc2f88b fix: kubectl apply in readme
* f00f057 chore: bump deps

<a name="v0.1.1"></a>
## [v0.1.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.1.0...v0.1.1) (2023-05-12)

Welcome to the v0.1.1 release of Proxmox CSI Plugin!

### Features

- switch to distroless
- decrease node image

### Changelog

* 429a420 chore: release v0.1.1
* 4e80caf chore: bump deps
* ff1c9bf feat: switch to distroless
* c437146 ci: test images
* be954c9 chore: bump deps
* 93a04b6 feat: decrease node image
* 4fe1ee4 doc: update readme

<a name="v0.1.0"></a>
## [v0.1.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.0.2...v0.1.0) (2023-05-04)

Welcome to the v0.1.0 release of Proxmox CSI Plugin!

### Bug Fixes

- release check

### Changelog

* 449bddf chore: release v0.1.0
* 303f430 refactor: rename storageID to storage
* 8ed6376 test: proxmox-api
* bc135db test: mock kubernetes-api
* ffa684f docs: helm readme
* c3bd4e7 fix: release check

<a name="v0.0.2"></a>
## [v0.0.2](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.01...v0.0.2) (2023-04-29)

Welcome to the v0.0.2 release of Proxmox CSI Plugin!

### Changelog

* 8390a9f chore: release v0.0.2

<a name="v0.01"></a>
## v0.01 (2023-04-29)

Welcome to the v0.01 release of Proxmox CSI Plugin!

### Bug Fixes

- raise condition during volume attach
- cluster schema

### Features

- resize pvc
- node daemon
- node daemonsets
- controller

### Changelog

* 56b4297 chore: release v0.0.1
* 82ee8e1 ci: check release
* da3bcc5 ci: github actions
* 27bf714 test: add more tests
* 112b7f9 test: add simple tests
* 45fc7e3 refactor: proxmox cloud config
* 6377ad2 docs: update readme
* 230ac1a refactor: volume funcs
* 3bf3ef5 fix: raise condition during volume attach
* 494a82b fix: cluster schema
* 0a3eaaa doc: update readme
* bd2c653 feat: resize pvc
* 4054a53 refactor: celanup and build
* 54dec7d feat: node daemon
* 269c708 feat: node daemonsets
* 0346c96 refactor: controller and node
* 9f0f7a3 feat: controller
