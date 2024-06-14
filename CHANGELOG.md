## [0.6.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.5.0...v0.6.0) (2024-04-13)


### Features

* **chart:** add initContainers and hostAliases ([769c008](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/769c008bb5cbd2a21303f12171d16df78000473e))
* **chart:** support setting annotations and labels on storageClasses ([a5f5add](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/a5f5adde7c3d533cae45660da312883b4ed1a24c))
* remove udev dependency ([1810ec7](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/1810ec71f56a03a462219a58f81c3d24e9fb1714))


### Bug Fixes

* cli migration ([41b19bd](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/41b19bdd40db9f84c0d65fe41983e2c2ee0b4977))
* deps update ([657ad00](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/657ad006c2e93f27ed08966c8e493cfb852a49ee))
* goreleaser ([04a40f4](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/04a40f40ebd6ef8904bdcc083ce028cf87544019))
* pvc migration ([ddfc362](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/ddfc36229ccdf2fdc29e9fff9e59463f4b2da866))

## [0.7.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.6.1...v0.7.0) (2024-06-14)


### Features

* swap pv in already created pvc ([76c899e](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/76c899e4e1c8f2287854a4e08fbbdd8a1c7360a9))
* wait volume to be detached ([3683d96](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/3683d96136a52d06274788006e0c5c67d9818c93))


### Bug Fixes

* implement structured logging ([cb5fb4e](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/cb5fb4e0339fa714e13e421e557252ab348dbe25))
* pv force migration ([8ecf990](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/8ecf990a9b746358a505c25c0c728fd77d776b00))

## [0.6.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.6.0...v0.6.1) (2024-04-13)


### Bug Fixes

* build release ([facdec5](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/facdec5be4f0335eb489fc600a175e63584953ba))
* release doc ([215c366](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/215c366b5bb4bc86e7bee43fcf4a228193fcbbbe))

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

- release v0.5.0 (be05e11)
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
