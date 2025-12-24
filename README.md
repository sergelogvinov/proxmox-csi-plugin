# Proxmox CSI Plugin

I have been using the `rancher.io/local-path` storage provisioner for over 3 years, and it has solved almost all of my problems.
However, in the event that the server needs maintenance such as rebooting, upgrading, or reinstalling,
I have to manage the PV myself by using tools like rsync, backup-restore, or other utilities to migrate the data to another server.

I am currently using persistent storage only for databases in the cluster, and in a high availability setup, all databases can replicate themselves.
That's why I do not need to use network storage, as database replication can recover data from the other replicas.
Additionally, using local disks provides me with better performance.

Nowadays, dedicated services are incredibly powerful.
However, bootstrapping a Kubernetes node with 40/80 CPUs and 128/256GB of RAM can be overwhelming and may present new challenges.
You would need to modify the default parameters in the kubelet configuration, which can be a complex process.
Therefore, I have opted to use the Proxmox hypervisor to launch two or more virtual machines (VMs) on a single physical server, whether it's for a homelab or a small production use case.

The Proxmox cloud has prompted me to consider the aging of local storage. It's often better to store local data on the hypervisor side rather than in the VM disk, as it offers greater flexibility. Migrating pods with persistent data between VMs within a Proxmox node becomes a much simpler process as a result.

This project aims to address this concept. All persistent volumes (PVs) will be created and stored on the Proxmox side, and pods will access the data as attached block devices.

This CSI plugin was designed to support multiple independent Proxmox clusters within a single Kubernetes cluster.
It enables the use of a single storage class to deploy one or many deployments/statefulsets across different regions, leveraging region/zone anti-affinity or topology spread constraints

## In Scope

* [Dynamic provisioning](https://kubernetes-csi.github.io/docs/external-provisioner.html): Volumes are created dynamically when `PersistentVolumeClaim` objects are created.
* [Topology](https://kubernetes-csi.github.io/docs/topology.html): feature to schedule Pod to Node where disk volume pool exists.
* Volume metrics: usage stats are exported as Prometheus metrics from `kubelet`.
* [Volume expansion](https://kubernetes-csi.github.io/docs/volume-expansion.html): Volumes can be expanded by editing `PersistentVolumeClaim` objects.
* [Storage capacity](https://kubernetes.io/docs/concepts/storage/storage-capacity/): Controller expose the Proxmox storade capacity.
* [Encrypted volumes](https://kubernetes-csi.github.io/docs/secrets-and-credentials-storage-class.html): Encryption with LUKS.
* [Volume bandwidth](https://pve.proxmox.com/wiki/Manual:_qm.conf): Maximum read/write limits.
* [Volume migration](docs/pvecsictl.md): Offline migration of PV to another Proxmox node (region).
* [Volume attributes class](docs/options.md): Detailed options for StorageClass.
* [Volume zone replication](docs/options.md): ZFS replication to another Proxmox node (zone).
* [Volume snapshots](https://kubernetes.io/docs/concepts/storage/volume-snapshots/): Create and restore volume snapshots. See [limits](docs/volumesnapshot.md) in the documentation.

## Overview


![ProxmoxClusers!](/docs/proxmox-regions.gif)

Arrows with dotted lines indicate the capability of attaching a Persistent Volume (PV) to a Pod.

- Each Proxmox cluster has predefined in cloud-config the region name (see `clusters[].region` below).
- Each Proxmox cluster has many Proxmox Nodes. In kubernetes scope it is called as `zone`. The name of `zone` is the name of Proxmox node.
- Pods can easily migrate between Kubernetes nodes on the same physical Proxmox node (`zone`).
  The PV will automatically be moved by the CSI Plugin.
- Pods with Persistent Volume (PV) allocated on `local storage (like: lvm, lvm-thin, zfs, xfs, ext4)` **cannot** automatically migrate across zones (Proxmox nodes).
  You can manually move PVs across zones using [pvecsictl](docs/pvecsictl.md) tool.
- Pods with Persistent Volume (PV) allocated on `shared disk (like: ceph, nfs)` **can** automatically migrate across zones (inside one region).

## Installation

To make use of the Proxmox CSI Plugin you need to correctly configure your Proxmox installation as well as your Kubernetes instance.

Requirements for Proxmox CSI Plugin
* Proxmox nodes must be clustered
* Proxmox CSI Plugin must have privileges in your Proxmox instance
* Kubernetes must be labelled with the correct topology
* A StoreClass referencing the CSI plugin exists

How to configure the above is detailed below

For details about how to install and deploy the CSI plugin, see [Installation instruction](docs/install.md).

### Proxmox VM Config:

For the Proxmox CSI Plugin to work you need to cluster your Proxmox nodes.
You can cluster a single Proxmox node with itself.
Read more about Proxmox clustering [here](https://pve.proxmox.com/wiki/Cluster_Manager).
Additionally, you can add [SMBIOS information](docs/options-node.md#cloud-init-smbios-custom-fields) to your VM configuration.

VM config after creating a Pod with PVC:

![VM](/docs/vm-disks.png)

`scsi2` disk on VM is a Kubernetes PV created by this CSI plugin.

For better performance use SCSI Controller - `VirtIO SCSI single`.

### Kubernetes Topology Labels

Proxmox CSI Plugin uses the well-known node labels to define the disk location:

* `topology.kubernetes.io/region` - the name must be the same as in cloud config region name
* `topology.kubernetes.io/zone` - proxmox node name
* `topology.proxmox.sinextra.dev/region` - alternative region label (optional)
* `topology.proxmox.sinextra.dev/node` - alternative zone label (optional)

Node spec:
* `Spec.ProviderID` - providerID magic string `proxmox://$REGION/$VMID` to help define the virtual machine ID, it cannot be changed after the first update. If it not exists, the plugin will find the VM by the name or UUID.

**Important**: The `topology.kubernetes.io/region` topology label **must** be set.
Region is the Proxmox cluster name in cloud config.

See [Node Annotations and Labels](docs/options-node.md) for more details.

The labels can be set manually using `kubectl`,
or automatically through a tool like [Proxmox CCM](https://github.com/sergelogvinov/proxmox-cloud-controller-manager).
I recommend using the CCM (Cloud Controller Manager).

### Storage Class Definition

Storage Class resource:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: proxmox-data-xfs
parameters:
  csi.storage.k8s.io/fstype: xfs|ext4
  storage: data
  cache: directsync|none|writeback|writethrough
  ssd: "true|false"
provisioner: csi.proxmox.sinextra.dev
allowVolumeExpansion: true
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
```

Storage parameters:
* `storage` - proxmox storage ID
* `cache` - qemu cache param: `directsync`, `none`, `writeback`, `writethrough` [Official documentation](https://pve.proxmox.com/wiki/Performance_Tweaks)
* `ssd` - true if SSD/NVME disk, which enables both SSD emulation and Discard options in Proxmox

For more detailed options and a comprehensive understanding, refer to the following link [StorageClass options](docs/options.md)

## Deployment examples

### Pod with ephemeral storage

Deploy a test Pod

```shell
kubectl apply -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/test-pod-ephemeral.yaml
```

Check status of PV and PVC

```shell
$ kubectl -n default get pods,pvc
NAME       READY   STATUS    RESTARTS   AGE
pod/test   1/1     Running   0          45s

NAME                             STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS      AGE
persistentvolumeclaim/test-pvc   Bound    pvc-5bc58ec7-da55-48d2-9dc5-75d4d6629a4e   1Gi        RWO            proxmox-data-xfs  45s

$ kubectl describe pv pvc-5bc58ec7-da55-48d2-9dc5-75d4d6629a4e
Name:              pvc-5bc58ec7-da55-48d2-9dc5-75d4d6629a4e
Labels:            <none>
Annotations:       pv.kubernetes.io/provisioned-by: csi.proxmox.sinextra.dev
                   volume.kubernetes.io/provisioner-deletion-secret-name:
                   volume.kubernetes.io/provisioner-deletion-secret-namespace:
Finalizers:        [kubernetes.io/pv-protection external-attacher/csi-proxmox-sinextra-dev]
StorageClass:      proxmox-data-xfs
Status:            Bound
Claim:             default/test-pvc
Reclaim Policy:    Delete
Access Modes:      RWO
VolumeMode:        Filesystem
Capacity:          1Gi
Node Affinity:
  Required Terms:
    Term 0:        topology.kubernetes.io/region in [Region-1]
                   topology.kubernetes.io/zone in [pve-1]
Message:
Source:
    Type:              CSI (a Container Storage Interface (CSI) volume source)
    Driver:            csi.proxmox.sinextra.dev
    FSType:            xfs
    VolumeHandle:      Region-1/pve-1/data/vm-9999-pvc-5bc58ec7-da55-48d2-9dc5-75d4d6629a4e
    ReadOnly:          false
    VolumeAttributes:      cache=writethrough
                           storage.kubernetes.io/csiProvisionerIdentity=1682607985217-8081-csi.proxmox.sinextra.dev
                           storage=data
```

### StatefulSet with persistent storage

```shell
kubectl apply -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/test-statefulset.yaml
```

Check status of PV and PVC

```shell
$ kubectl -n default get pods,pvc -owide
NAME         READY   STATUS    RESTARTS   AGE   IP             NODE        NOMINATED NODE   READINESS GATES
pod/test-0   1/1     Running   0          27s   10.32.8.251    worker-11   <none>           <none>
pod/test-1   1/1     Running   0          27s   10.32.13.202   worker-31   <none>           <none>
pod/test-2   1/1     Running   0          26s   10.32.2.236    worker-12   <none>           <none>
pod/test-3   1/1     Running   0          26s   10.32.14.20    worker-32   <none>           <none>

NAME                                   STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS      AGE   VOLUMEMODE
persistentvolumeclaim/storage-test-0   Bound    pvc-3b76c8aa-1024-4f2e-88ca-8b3e27e27f65   1Gi        RWO            proxmox-data-xfs  27s   Filesystem
persistentvolumeclaim/storage-test-1   Bound    pvc-70394f08-db69-435f-a373-6c4526732042   1Gi        RWO            proxmox-data-xfs  27s   Filesystem
persistentvolumeclaim/storage-test-2   Bound    pvc-8a64b28e-826c-4ece-84f7-7bd921250881   1Gi        RWO            proxmox-data-xfs  26s   Filesystem
persistentvolumeclaim/storage-test-3   Bound    pvc-847c1eca-1c1f-4be3-ba15-754785ffe4ad   1Gi        RWO            proxmox-data-xfs  26s   Filesystem

$ kubectl describe pv pvc-3b76c8aa-1024-4f2e-88ca-8b3e27e27f65
Name:              pvc-3b76c8aa-1024-4f2e-88ca-8b3e27e27f65
Labels:            <none>
Annotations:       pv.kubernetes.io/provisioned-by: csi.proxmox.sinextra.dev
                   volume.kubernetes.io/provisioner-deletion-secret-name:
                   volume.kubernetes.io/provisioner-deletion-secret-namespace:
Finalizers:        [kubernetes.io/pv-protection external-attacher/csi-proxmox-sinextra-dev]
StorageClass:      proxmox
Status:            Bound
Claim:             default/storage-test-0
Reclaim Policy:    Delete
Access Modes:      RWO
VolumeMode:        Filesystem
Capacity:          1Gi
Node Affinity:
  Required Terms:
    Term 0:        topology.kubernetes.io/zone in [pve-1]
                   topology.kubernetes.io/region in [Region-1]
Message:
Source:
    Type:              CSI (a Container Storage Interface (CSI) volume source)
    Driver:            csi.proxmox.sinextra.dev
    FSType:            xfs
    VolumeHandle:      Region-1/pve-1/data/vm-9999-pvc-3b76c8aa-1024-4f2e-88ca-8b3e27e27f65
    ReadOnly:          false
    VolumeAttributes:      cache=writethrough
                           storage.kubernetes.io/csiProvisionerIdentity=1682607985217-8081-csi.proxmox.sinextra.dev
                           storage=data
```

## Disk usage capacity

Check existence of CSIDriver

```shell
$ kubectl get CSIDriver
NAME                        ATTACHREQUIRED   PODINFOONMOUNT   STORAGECAPACITY   TOKENREQUESTS   REQUIRESREPUBLISH   MODES                  AGE
csi.proxmox.sinextra.dev    true             true             true              <unset>         false               Persistent             47h
```

Check Proxmox pool capacity.
Available capacity should be non-zero size.

```shell
$ kubectl get csistoragecapacities -ocustom-columns=CLASS:.storageClassName,AVAIL:.capacity,ZONE:.nodeTopology.matchLabels -A
CLASS              AVAIL       ZONE
proxmox-data-xfs   470268Mi    map[topology.kubernetes.io/region:Region-2 topology.kubernetes.io/zone:pve-3]
proxmox-data-xfs   5084660Mi   map[topology.kubernetes.io/region:Region-1 topology.kubernetes.io/zone:pve-1]
```

Check node CSI drivers on a node

```shell
$ kubectl get CSINode worker-11 -oyaml
apiVersion: storage.k8s.io/v1
kind: CSINode
metadata:
  name: worker-11
spec:
  drivers:
  - allocatable:
      count: 16
    name: csi.proxmox.sinextra.dev
    nodeID: worker-11
    topologyKeys:
    - topology.kubernetes.io/region
    - topology.kubernetes.io/zone
```

## FAQ

See [FAQ](docs/faq.md) for answers to common questions.

## Resources

* https://arslan.io/2018/06/21/how-to-write-a-container-storage-interface-csi-plugin/
* https://kubernetes-csi.github.io/docs/
* https://pve.proxmox.com/wiki/Manual:_qm.conf
* https://pve.proxmox.com/wiki/Performance_Tweaks
* https://kb.blockbridge.com/guide/proxmox/
* https://github.com/sergelogvinov/ansible-role-proxmox
* https://github.com/sergelogvinov/terraform-talos/tree/main/proxmox

## Contributing

Contributions are welcomed and appreciated!
See [Contributing](CONTRIBUTING.md) for our guidelines.

## License

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

---

`Proxmox®` is a registered trademark of [Proxmox Server Solutions GmbH](https://www.proxmox.com/en/about/company).
