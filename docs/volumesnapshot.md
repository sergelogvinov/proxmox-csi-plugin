# Volume Snapshot

Proxmox VE does not support volume snapshots natively. However, the Proxmox CSI Driver provides a way to create and manage volume snapshots using Kubernetes `VolumeSnapshot` resources.

`Warning`: Note this is `experimental` feature and requires root access to Proxmox API (root@pam). All parameters and features may change in future releases.

The snapshot created using this method is a full copy of the original volume, not a delta snapshot. This means that the snapshot will consume the same amount of storage as the original volume.

## Prerequirements

Update your Proxmox CSI Driver configuration to include all clusters where you want to enable volume snapshot support.
Make sure to use the `root@pam` account for this feature to function properly.

```yaml
clusters:
  - url: https://cluster-api-1.exmple.com:8006/api2/json
    username: root@pam
    password: "your_password"
    ...
```

## Volume Snapshot Class

Create a Kubernetes `VolumeSnapshotClass` to define how snapshots are created, and set its driver to the Proxmox CSI Driver.

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: snapshot-class-name
parameters:
  # Optional: specify zone to copy snapshots in a specific zone
  zone: rnd-2
driver: csi.proxmox.sinextra.dev
deletionPolicy: Delete
```

### Parameters:

* `zone`: (Optional) Specify the zone name to create snapshots within a specific availability zone. If not specified, the snapshot will be created in the same zone as the source volume.

### DeletionPolicy

The deletion policy determines what happens to a volume snapshot when its associated VolumeSnapshotClass is removed.
There are two possible policies:

* `Retain`: The VolumeSnapshotContent remains after the VolumeSnapshotClass is deleted. An administrator must manually delete or reclaim it
* `Delete`: The VolumeSnapshotContent is automatically deleted when the VolumeSnapshotClass is deleted.

## Creating a Volume Snapshot

To create a volume snapshot, you need to create a `VolumeSnapshot` resource that references the `VolumeSnapshotClass` you created earlier.

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: snapshot-name
  namespace: default
spec:
  volumeSnapshotClassName: snapshot-class-name
  source:
    persistentVolumeClaimName: pvc-source-name
```

Its takes some time to create the snapshot. You can check the status of the snapshot using the following command:

```bash
kubectl -n default get volumesnapshot snapshot-name
```

Output will look like:

```shell
NAME   READYTOUSE   SOURCEPVC          SOURCESNAPSHOTCONTENT   RESTORESIZE   SNAPSHOTCLASS   SNAPSHOTCONTENT                                    CREATIONTIME   AGE
test   false        pvc-source-name                                          proxmox         snapcontent-e56c938a-3ead-4221-bb76-aa4aaf149dc2   52s            115s
```

Once the snapshot is ready to use, the `READYTOUSE` column will change to `true`.

Now you can create a new PersistentVolumeClaim from the snapshot:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: storage-test-2-restore
  namespace: default
spec:
  storageClassName: proxmox-zfs
  dataSource:
    apiGroup: snapshot.storage.k8s.io
    kind: VolumeSnapshot
    name: snapshot-name
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
```

Make sure the size of the new PVC is equal to or larger than the original PVC from which the snapshot was created.
The restore process begins automatically once the PersistentVolumeClaim is attached to a Pod

## Creating a PersistentVolumeClaim from an Existing PersistentVolumeClaim

You can also create a new PersistentVolumeClaim by cloning an existing PersistentVolumeClaim.
This is done by specifying the source PersistentVolumeClaim in the `dataSource` field of the new PersistentVolumeClaim.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: storage-test-2-restore
  namespace: default
spec:
  storageClassName: proxmox-zfs
  dataSource:
    kind: PersistentVolumeClaim
    name: storage-test-2-0
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 55Gi
```
