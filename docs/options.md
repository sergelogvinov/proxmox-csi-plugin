# Node

Individual Kubernetes Nodes can be configured using a set of labels specific to the driver.

```yaml
apiVersion: v1
kind: Node
metadata:
  ...
  labels:
    # Maximum number of volumes that can be attached to this node, default is 24
    # Note: Currently, there is a maximum limit of 30 virtio iscsi volumes *total*, including root disks, that can be attached to a single VM in QEMU/Proxmox.
    csi.proxmox.sinextra.dev/max-volume-attachments = "24"
...
```

# Storage Class

A Kubernetes StorageClass is an object that defines the storage "classes" or tiers available for dynamic provisioning of storage volumes in a Kubernetes cluster. It abstracts the underlying storage infrastructure, making it easier for developers and administrators to manage persistent storage for applications running in Kubernetes.

Deploy examples you can find [here](deploy/).

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: proxmox-storage-class-name
  annotations:
    # If you need to set it as default
    storageclass.kubernetes.io/is-default-class: "true"
parameters:
  # Pre defined options
  ## File system format (default: ext4)
  csi.storage.k8s.io/fstype: ext4|xfs

  ## Optional: If you want to encrypt the disk
  csi.storage.k8s.io/node-stage-secret-name: "proxmox-csi-secret"
  csi.storage.k8s.io/node-stage-secret-namespace: "kube-system"
  ## Have to be the same as node-stage-secret-* if you want to expand the volume
  csi.storage.k8s.io/node-expand-secret-name: "proxmox-csi-secret"
  csi.storage.k8s.io/node-expand-secret-namespace: "kube-system"

  ## Optional: File system format options
  blockSize: "4096"
  inodeSize: "256"

  # Proxmox csi options
  ## Proxmox storage ID
  storage: data

  ## Optional: Proxmox csi options
  cache: directsync|none|writeback|writethrough
  ssd: "true|false"

  ## Optional: Proxmox disk speed limit
  diskIOPS: "4000"
  diskMBps: "1000"

  ## Optional: Zone replication
  replicate: "true"
  replicateSchedule: "*/15"
  replicateZones: "pve-1,pve-3"

# Optional: This field allows you to specify additional mount options to be applied when the volume is mounted on the node
mountOptions:
  # Common for ssd
  - noatime

# Optional: Allowed topologies restricts what nodes this StorageClass can be used on
allowedTopologies:
- matchLabelExpressions:
  # Region have to be exist, if you want to use allowedTopologies
  - key: topology.kubernetes.io/region
    values:
    - Region-1
  # Better to set zone, otherwise it will be used random node in the region
  - key: topology.kubernetes.io/zone
    values:
    - pve-1
    - pve-3

provisioner: csi.proxmox.sinextra.dev
allowVolumeExpansion: true
reclaimPolicy: Delete|Retain
volumeBindingMode: WaitForFirstConsumer|Immediate
```

You can use `VolumeAttributesClass` to redefine the mutable parameters of the `StorageClass`.

```yaml
apiVersion: storage.k8s.io/v1beta1
kind: VolumeAttributesClass
metadata:
  name: proxmox-attributes
driverName: csi.proxmox.sinextra.dev
parameters:
  ## Optional: Proxmox disk speed limit
  diskIOPS: "4000"
  diskMBps: "1000"

  ## Optional: Backup disk with VM
  backup: "true"

  ## Optional: Zone replication
  replicateSchedule: "*/30"
  replicateZones: "rnd-1,rnd-2"
```

## Parameters:

* `node-stage-secret-name`/`node-expand-secret-name`,  `node-stage-secret-namespace`/`node-expand-secret-namespace` - Refer to the name and namespace of the Secret object in the Kubernetes API. The secrets key name is `encryption-passphrase`. [Official documentation](https://kubernetes-csi.github.io/docs/secrets-and-credentials-storage-class.html)

```yaml
apiVersion: v1
data:
  encryption-passphrase: base64-encode
kind: Secret
metadata:
  name: proxmox-csi-secret
  namespace: kube-system
```

* `blockSize` - specify the size of blocks in bytes.
* `inodeSize` - Specify the size of each inode in bytes.

* `storage` - proxmox storage ID
* `cache` - qemu cache param: `directsync`, `none`, `writeback`, `writethrough` [Official documentation](https://pve.proxmox.com/wiki/Performance_Tweaks)
* `ssd` - set true if SSD/NVME disk

* `diskIOPS` - maximum r/w I/O in operations per second
* `diskMBps` - maximum r/w throughput in megabytes per second

* `backup` - set true if you want to backup the disk with VM. Dangerous option! Do not use it unless you fully understand how to use it in the recovery process.

* `replicate` - set true if you want to replicate the disk to another zone
* `replicateSchedule` - replication schedule [in systemd calendar format](https://pve.proxmox.com/pve-docs/pve-admin-guide.html#pvesr_schedule_time_format) (default: `*/15`)
* `replicateZones` - zones where the disk will be replicated, separated by commas

## AllowVolumeExpansion

Allow you to resize (expand) the PVC in future.

## ReclaimPolicy

It defines what happens to the storage volume when the associated PersistentVolumeClaim (PVC) is deleted. There are three reclaim policies:

* `Retain`: The storage volume is not deleted when the PVC is released, and it must be manually reclaimed by an administrator.
* `Delete`: The storage volume is deleted when the PVC is released.

## VolumeBindingMode

It specifies how volumes should be bound to PVs (Persistent Volumes). There are two modes:

* `Immediate`: PVs are bound as soon as a PVC is created, even if a suitable storage volume isn't immediately available. This is suitable for scenarios where waiting for storage is not an option.
* `WaitForFirstConsumer`: PVs are bound only when a pod using the PVC is scheduled. This is useful when you want to ensure that storage is provisioned only when it's actually needed.
