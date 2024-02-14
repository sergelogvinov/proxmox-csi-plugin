## [v0.4.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.4.0...v0.4.1) (2024-01-01)

Welcome to the v0.4.1 release of Proxmox CSI Plugin!

### Bug Fixes
- publish shared volumes ([a681b2b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/a681b2b2b6431a23ef45e1705b0adb85dca34f5b))
- find zone by region ([4eae22d](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/4eae22d81c2ccd08a159f04412918deb09e19ab5))

### Features
- **chart:** add value to customize kubeletDir ([bbb627f](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/bbb627f1a871f483f47888f2b158ad3370926dd0))
- **chart:** add allowedTopologies ([41cb02a](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/41cb02a16dfa1b6abc2d2023d0d01f59a9501c8f))

### Miscellaneous
- release v0.4.1 ([fd8d14f](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/fd8d14f3ce5c53519353d9aa5fe69de261a3fd08))
- bump deps ([2a86bd7](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/2a86bd7ef3b3291ab335ccec6760265d0fac5e7c))
- bump deps ([d8c98ea](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/d8c98ea0b51445eddeee0b5a34c160b2d10b6cfa))
- bump deps ([9054282](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/9054282fb8a6ab1748472a05f4e00f56e2415fd2))

## [v0.4.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.3.0...v0.4.0) (2023-10-24)

Welcome to the v0.4.0 release of Proxmox CSI Plugin!

### Bug Fixes
- check volume existence ([aba0ca8](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/aba0ca8fb4d96af8324de03856a55a8f130311e3))
- helm create namespace ([364b8be](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/364b8bee9342e8c6437078dfc0488784d8a41000))
- remove nocloud label ([74e42b2](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/74e42b2818d199fd4058f7dbd26eea1e4f70647a))

### Features
- mkfs block/inode size options ([88f4ebc](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/88f4ebcf33cef7409090383b68bbec6f8d52ffe5))
- disk speed limit ([c464dab](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/c464dabb186f1a451057d1ba7c736a919b607c59))
- **chart:** make StorageClass parameters/mountOptions configurable ([a78e338](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/a78e338d3432d176ea543a3a455cfbcc7b1f5676))

### Miscellaneous
- release v0.4.0 ([764b741](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/764b74191d2b246518047125ec083455e5564e6a))
- bump deps ([9e5a139](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/9e5a13927a243a79770d31b32e03c0ad70bdba4b))
- bump deps ([a243ffb](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/a243ffb48b6fd7d7969fd957cd54dffab51fe71e))

## [v0.3.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.2.0...v0.3.0) (2023-09-19)

Welcome to the v0.3.0 release of Proxmox CSI Plugin!

### Features
- storage encryption ([26c1928](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/26c19288b80ed2847ab0df63e5480cf4f1f059e4))
- volume capability ([1088dbb](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/1088dbb8297802a52835bc4075b6553cb7e8a81a))
- regional block devices ([c7d1541](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/c7d1541d4d8f0818b6af27e497746415c1448e03))

### Miscellaneous
- release v0.3.0 ([324ad91](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/324ad9155d9f62161323dc037243b0488474c23d))
- bump deps ([5f5d781](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/5f5d7813b494dd9d2334e4707c11d409c4754006))
- bump actions/checkout from 3 to 4 ([f75bfff](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/f75bfff3573d970f4442cb1cf88962b7544e8a92))
- bump sigstore/cosign-installer from 3.1.1 to 3.1.2 ([51419d3](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/51419d33d16c137624349fb874200a70f47b8e1c))
- bump deps ([ae63a06](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/ae63a06de032ff5911517730987046ae812534e0))
- bump deps ([4ceef77](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/4ceef773a16ec3eb7496f4538a75552f3c4add1c))

## [v0.2.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.1.1...v0.2.0) (2023-08-07)

Welcome to the v0.2.0 release of Proxmox CSI Plugin!

### Bug Fixes
- skip lxc containers on resize process ([a24d24e](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/a24d24e6ac81f76f06adc9d834beba769975d055))
- helm liveness context ([e1ed889](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/e1ed889510ce2309f2f63243c95e50b03c50254e))
- detach volume error ([dc128d1](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/dc128d17ab1e298bff267d7b776e893ba7929d3e))
- kubectl apply in readme ([bc2f88b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/bc2f88bdd01c68be2be620af464461459279f642))

### Features
- noatime flag for ssd ([cd4f3f7](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/cd4f3f77d38ef1e6b34c337c96b3b60fd2f04007))
- cosign images ([5e13f3f](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/5e13f3f3874509484609350ecca3e9bd03ca4417))
- pin version ([e81d8e3](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/e81d8e326cd48ebd2cc2578d88bccc93d5c421ab))
- helm oci release ([c438712](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/c4387124372a08f2a31dd0934c9cf41b67f12eba))
- drop node capabilities ([927f664](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/927f6641a762bcf43556eaf0688fc684106002a5))
- trim filesystem ([dc7dbbd](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/dc7dbbdb6e9deecab0abd439c77abffdd69f692a))

### Miscellaneous
- release v0.2.0 ([6a2d98a](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/6a2d98a7dbba9f4da83a6dda7cbdf91142091239))
- bump actions versions ([b477132](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/b477132e831b52d284964c79e81396d90ff5421d))
- bump deps ([f6d726c](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/f6d726c0a7ea4e3b9a7265aee0bfb351e6e96e84))
- bump deps ([ecea2ad](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/ecea2ad9c459cb00ab495e4ceed9ab3e23d8bb6e))
- bump deps ([28f0a72](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/28f0a72b8f6727597ca3051829bafc50cc9963b2))
- bump deps ([f00f057](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/f00f0573a25053fb398d12fcddd75883204463f4))

## [v0.1.1](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.1.0...v0.1.1) (2023-05-12)

Welcome to the v0.1.1 release of Proxmox CSI Plugin!

### Features
- switch to distroless ([ff1c9bf](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/ff1c9bf44798c28b3888e6eab6eabcddc203a57a))
- decrease node image ([93a04b6](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/93a04b67ba8b11f7bf1a587a066552039b1d57d9))

### Miscellaneous
- release v0.1.1 ([429a420](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/429a420f9b606e0d41d361887f4e08997aa2a67d))
- bump deps ([4e80caf](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/4e80cafbc9198cb6e34a082ba8833efe3db16814))
- bump deps ([be954c9](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/be954c9da6feaa9e8a537bea94004146ebc53d3d))

## [v0.1.0](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.0.2...v0.1.0) (2023-05-04)

Welcome to the v0.1.0 release of Proxmox CSI Plugin!

### Bug Fixes
- release check ([c3bd4e7](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/c3bd4e72376ad622172423da3be4f089216265b5))

### Miscellaneous
- release v0.1.0 ([449bddf](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/449bddf6658be6c3df0f41b4a69d43b15141e447))

## [v0.0.2](https://github.com/sergelogvinov/proxmox-csi-plugin/compare/v0.01...v0.0.2) (2023-04-29)

Welcome to the v0.0.2 release of Proxmox CSI Plugin!

### Miscellaneous
- release v0.0.2 ([8390a9f](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/8390a9f04bda546afb29e5b48513c4f64a906ffb))

## v0.01 (2023-04-29)

Welcome to the v0.01 release of Proxmox CSI Plugin!

### Bug Fixes
- raise condition during volume attach ([3bf3ef5](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/3bf3ef5c72c93a6a965e62a7cc5e38de1e0732a9))
- cluster schema ([494a82b](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/494a82b3dcfd8ea3cd0a952d766f27631b6e7d65))

### Features
- resize pvc ([bd2c653](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/bd2c65362355d2cb3bc05a67ec29cf4e6cd6461c))
- node daemon ([54dec7d](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/54dec7dafe79f51c8d0a99a205ddc1fdd5bbab26))
- node daemonsets ([269c708](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/269c7080fa302151ea281a74bb9b1757e3ec8d36))
- controller ([9f0f7a3](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/9f0f7a325b1f06e611deca18808dcdcc4bf7e5ef))

### Miscellaneous
- release v0.0.1 ([56b4297](https://github.com/sergelogvinov/proxmox-csi-plugin/commit/56b42975117f0dff0ccec4dd9e15bada125e5aef))

