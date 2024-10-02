# Fast answers to common questions

## Can PV/PVC migrate between Proxmox nodes?

The __local storages__ can't be migrated between Proxmox nodes automatically.
But you can do it manually by following tool [pvecsictl](../docs/pvecsictl.md)

The __shared storages__ like nfs, ceph can be migrated between Proxmox nodes automatically.

## Create a PV/PVC with already existing disk

If you have a disk already created in Proxmox, you can use it with the CSI plugin.
First, you need to create a PV/PVC with special disk name and the storage class name.
The size of the PersistentVolume must be the same as the disk size.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pvc-test
spec:
  accessModes:
    - ReadWriteOnce
  capacity:
    storage: 10Gi
  csi:
    driver: csi.proxmox.sinextra.dev
    fsType: xfs
    volumeAttributes:
      storage: zfs
    volumeHandle: dev-1/pve-m-4/zfs/vm-100-disk-1
  storageClassName: proxmox-zfs
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: storage-test-0
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  storageClassName: proxmox-zfs
  volumeName: pvc-test
```

The disk name is `vm-100-disk-1`, and the storage class name is `proxmox-zfs`.

## How to change encrypted disk secret key?

The secret key cannot be change through kubernetes API, but you can use the following instructions.
Before starting, read the good explanation of the [cryptsetup](https://wiki.archlinux.org/title/Dm-crypt/Device_encryption) tool.

First, you need to run the pod with secured PVC, then you need to define the csi-plugin pod running on the same node as the pod with the PVC.

Lets say the csi-plugin pod name is `proxmox-csi-plugin-node-hjchn` and `/dev/sdb` is the disk you want to change the secret key for.

```shell
# Check the disk
kubectl -n csi-proxmox exec -ti proxmox-csi-plugin-node-hjchn -- /sbin/cryptsetup luksDump /dev/sdb
# Check the passphrase
kubectl -n csi-proxmox exec -ti proxmox-csi-plugin-node-hjchn -- /sbin/cryptsetup luksOpen --test-passphrase -v /dev/sdb
```

Add the new passphrase to the disk

```shell
# Add the new passphrase
kubectl -n csi-proxmox exec -ti proxmox-csi-plugin-node-hjchn -- /sbin/cryptsetup luksAddKey /dev/sdb
# Check the new passphrase
kubectl -n csi-proxmox exec -ti proxmox-csi-plugin-node-hjchn -- /sbin/cryptsetup luksOpen --test-passphrase -v /dev/sdb
# Check the disk
kubectl -n csi-proxmox exec -ti proxmox-csi-plugin-node-hjchn -- /sbin/cryptsetup luksDump /dev/sdb
```

After you have added the new passphrase, you can remove the old one.
And change the passphrase in kubernetes secret resource.

```shell
# Remove the old passphrase
kubectl -n csi-proxmox exec -ti proxmox-csi-plugin-node-hjchn -- /sbin/cryptsetup luksRemoveKey /dev/sdb
```
