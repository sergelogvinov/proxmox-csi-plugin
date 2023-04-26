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

## Overview

![ProxmoxClusers!](/docs/proxmox-regions.jpeg)

- Each Proxmox cluster has predefined in cloud-config the region name (see `clusters[].region` below).
- Each Proxmox Cluster has many Proxmox Nodes. In kubernetes scope it is called as `zone`. The name of `zone` is the name of Proxmox node.
- The Pods can easyly migrade inside the Proxmox node with PV. PV will reattache to anothe VM by CSI Plugin.
- The Pod `cannot` migrate to another zone (another Proxmox node)

CSI Plugin uses the well-known node labels/spec to define the location
* topology.kubernetes.io/region
* topology.kubernetes.io/zone
* Spec.ProviderID

**Caution**: set the labels `topology.kubernetes.io/region` and `topology.kubernetes.io/zone` are very important.
You can set it by `kubectl` or use [Proxmox CCM](https://github.com/sergelogvinov/proxmox-cloud-controller-manager).
It uses the case Proxmox Cloud config.
And it labels the node properly.
I recommend using the CCM (Cloud Controller Manager).

## Install CSI Driver

Proxmox cloud config (the same as Proxmox CCM uses):

```yaml
clusters:
  - url: https://cluster-api-1.exmple.com:8006/api2/json
    insecure: false
    token_id: "login!name"
    token_secret: "secret"
    region: Region-1
  - url: https://cluster-api-2.exmple.com:8006/api2/json
    insecure: false
    token_id: "login!name"
    token_secret: "secret"
    region: Region-2
```

### Method 1: By Kubectl

```shell
kubectl -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/proxmox-csi-plugin.yml
```

### Method 2: By Helm

```shell
helm upgrade -i -n csi-proxmox proxmox-csi-plugin charts/proxmox-csi-plugin
```

### Method 3: Talos machine config

```yaml
cluster:
  externalCloudProvider:
    enabled: true
    manifests:
      - https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/proxmox-csi-plugin-talos.yml
```

## Deployment examples

### Pod with ephemeral storage

Deploy the pod

```shell
kubectl -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/test-pod-ephemeral.yaml
```

Check status of PV,PVC

```shell
```

### Statefulset with persistent storage

```shell
kubectl -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/test-statefulset.yaml
```

Check status of PV,PVC

```shell
```

### Usage

Check existence of CSIDriver

```shell
$ kubectl get CSIDriver
NAME                        ATTACHREQUIRED   PODINFOONMOUNT   STORAGECAPACITY   TOKENREQUESTS   REQUIRESREPUBLISH   MODES                  AGE
csi.proxmox.sinextra.dev    true             true             true              <unset>         false               Persistent             47h
```

Check Proxmox pool capacity

```shell
$ kubectl get csistoragecapacities -ocustom-columns=CLASS:.storageClassName,AVAIL:.capacity,ZONE:.nodeTopology.matchLabels -A
CLASS              AVAIL       ZONE
proxmox-data-xfs   4234740Mi   map[topology.kubernetes.io/region:cluster-1 topology.kubernetes.io/zone:pve-1]
```

Check node CSI drivers on the node

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

## Definition

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: proxmox-data-xfs
parameters:
  csi.storage.k8s.io/fstype: xfs|ext4
  storageID: data
  cache: directsync|none|writeback|writethrough
  ssd: true|false
  discard: ignore|on
provisioner: csi.proxmox.sinextra.dev
allowVolumeExpansion: true
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
```

Storage parameters:
* storageID - proxmox storage ID
* cache - qemu cache param: `directsync`, `none`, `writeback`, `writethrough` see [official documentation](https://pve.proxmox.com/wiki/Performance_Tweaks)
* ssh - true if SSD/NVME disk
* discard - use SSD/NVME discard command

## In Scope

* [Dynamic provisioning](https://kubernetes-csi.github.io/docs/external-provisioner.html): Volumes are created dynamically when `PersistentVolumeClaim` objects are created.
* [Topology](https://kubernetes-csi.github.io/docs/topology.html): feature to schedule Pod to Node where disk volume exists.
* Volume metrics: usage stats are exported as Prometheus metrics from `kubelet`.
* [Volume Expansion](https://kubernetes-csi.github.io/docs/volume-expansion.html): Volumes can be expanded by editing `PersistentVolumeClaim` objects.
* [Storage capacity](https://kubernetes.io/docs/concepts/storage/storage-capacity/): Controller expose the Proxmox storade capacity.

### Planned features

* [Encrypted volumes](https://kubernetes-csi.github.io/docs/secrets-and-credentials-storage-class.html): encryption with LUKS

## Resources

* https://arslan.io/2018/06/21/how-to-write-a-container-storage-interface-csi-plugin/
* https://kubernetes-csi.github.io/docs/
* https://pve.proxmox.com/wiki/Manual:_qm.conf
* https://pve.proxmox.com/wiki/Performance_Tweaks
* https://kb.blockbridge.com/guide/proxmox/

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
