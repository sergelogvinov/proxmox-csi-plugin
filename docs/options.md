# Storage Class

A Kubernetes StorageClass is an object that defines the storage "classes" or tiers available for dynamic provisioning of storage volumes in a Kubernetes cluster. It abstracts the underlying storage infrastructure, making it easier for developers and administrators to manage persistent storage for applications running in Kubernetes.

Deploy examples you can find [here](deploy/).

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: proxmox-data-xfs

parameters:
  # Pre defined options
  csi.storage.k8s.io/fstype: xfs|ext4
  ## If you want to encrypt the disk
  csi.storage.k8s.io/node-stage-secret-name: "proxmox-csi-secret"
  csi.storage.k8s.io/node-stage-secret-namespace: "kube-system"
  csi.storage.k8s.io/node-expand-secret-name: "proxmox-csi-secret"
  csi.storage.k8s.io/node-expand-secret-namespace: "kube-system"

  # Proxmox csi options
  storage: data
  cache: directsync|none|writeback|writethrough
  ssd: "true|false"

# This field allows you to specify additional mount options to be applied when the volume is mounted on the node
mountOptions:
  # Common for ssd
  - noatime

provisioner: csi.proxmox.sinextra.dev
allowVolumeExpansion: true
reclaimPolicy: Delete|Retain
volumeBindingMode: WaitForFirstConsumer|Immediate
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

* `storage` - proxmox storage ID
* `cache` - qemu cache param: `directsync`, `none`, `writeback`, `writethrough` [Official documentation](https://pve.proxmox.com/wiki/Performance_Tweaks)
* `ssd` - true if SSD/NVME disk

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
