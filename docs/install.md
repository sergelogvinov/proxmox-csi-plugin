# Install plugin

This plugin allows Kubernetes to use `Proxmox VE` storage as a persistent storage solution for stateful applications.
Supported storage types:
- Directory
- LVM
- LVM-thin
- ZFS
- NFS
- Ceph

## Proxmox configuration

Proxmox CSI Plugin requires the correct privileges in order to allocate and attach disks.

Create `CSI` role in Proxmox:

```shell
pveum role add CSI -privs "VM.Audit VM.Config.Disk Datastore.Allocate Datastore.AllocateSpace Datastore.Audit"
# Or if you need to use Replication feature
pveum role add CSI -privs "VM.Audit VM.Config VM.Allocate Datastore.Allocate Datastore.AllocateSpace Datastore.Audit"
```

Next create a user `kubernetes-csi@pve` for the CSI plugin and grant it the above role

```shell
pveum user add kubernetes-csi@pve
pveum aclmod / -user kubernetes-csi@pve -role CSI
pveum user token add kubernetes-csi@pve csi -privsep 0
```

All VMs in the cluster must have the `SCSI Controller` set to `VirtIO SCSI single` or `VirtIO SCSI` type to be able to attach disks.

## Install CSI Driver

Create a namespace `csi-proxmox` for the plugin and grant it the `privileged` permissions

```shell
kubectl create ns csi-proxmox
kubectl label ns csi-proxmox pod-security.kubernetes.io/enforce=privileged
```

All examples below assume that plugin controller runs on control-plane. Change the `nodeSelector` to match your environment if needed.

```yaml
nodeSelector:
  node-role.kubernetes.io/control-plane: ""
tolerations:
  - key: node-role.kubernetes.io/control-plane
    effect: NoSchedule
```

### Install the plugin by using kubectl

Create a Proxmox cloud config to connect to your cluster with the Proxmox user you just created.
More information about the configuration can be found in [config.md](config.md).

```yaml
# config.yaml
clusters:
  # List of Proxmox clusters

  - url: https://cluster-api-1.exmple.com:8006/api2/json
    # Skip the certificate verification, if needed
    insecure: false
    # Proxmox api token
    token_id: "kubernetes-csi@pve!csi"
    token_secret: "secret"
    # Region name, which is cluster name
    region: Region-1

  # Add more clusters if needed
  - url: https://cluster-api-2.exmple.com:8006/api2/json
    insecure: false
    token_id: "kubernetes-csi@pve!csi"
    token_secret: "secret"
    region: Region-2
```

Upload the configuration to the Kubernetes as a secret

```shell
kubectl -n csi-proxmox create secret generic proxmox-csi-plugin --from-file=config.yaml
```

Install latest release version

```shell
kubectl apply -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/proxmox-csi-plugin-release.yml
```

Or install latest stable version (edge)

```shell
kubectl apply -f https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/proxmox-csi-plugin.yml
```

### Install the plugin by using Helm

Create the helm values file, for more information see [values.yaml](../charts/proxmox-csi-plugin/values.yaml)

```yaml
# proxmox-csi.yaml
config:
  clusters:
    - url: https://cluster-api-1.exmple.com:8006/api2/json
      insecure: false
      token_id: "kubernetes-csi@pve!csi"
      token_secret: "secret"
      region: Region-1
    # Add more clusters if needed
    - url: https://cluster-api-2.exmple.com:8006/api2/json
      insecure: false
      token_id: "kubernetes-csi@pve!csi"
      token_secret: "secret"
      region: Region-2

# Define the storage classes
storageClass:
  - name: proxmox-data-xfs
    storage: data
    reclaimPolicy: Delete
    fstype: xfs
    # Define the storage class as default
    annotations:
      storageclass.kubernetes.io/is-default-class: "true"
```

Install the plugin. You need to prepare the `csi-proxmox` namespace first, see above

```shell
helm upgrade -i -n csi-proxmox -f proxmox-csi.yaml proxmox-csi-plugin oci://ghcr.io/sergelogvinov/charts/proxmox-csi-plugin
```

#### Option for k0s

If you're running [k0s](https://k0sproject.io/) you need to add extra value to the helm chart

```yaml
kubeletDir: /var/lib/k0s/kubelet
```

#### Option for microk8s

If you're running [microk8s](https://microk8s.io/) you need to add extra value to the helm chart

```yaml
kubeletDir: /var/snap/microk8s/common/var/lib/kubelet
```

### Install the plugin by using Talos machine config

If you're running [Talos](https://www.talos.dev/) you can install Proxmox CSI plugin using the machine config

```yaml
cluster:
  externalCloudProvider:
    enabled: true
    manifests:
      - https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/proxmox-csi-plugin.yml
```

Or all together with the Proxmox Cloud Controller Manager

* Proxmox CCM will label the nodes
* Proxmox CSI will use the labeled nodes to define the regions and zones

```yaml
cluster:
  inlineManifests:
    - name: proxmox-cloud-controller-manager
      contents: |-
        apiVersion: v1
        kind: Secret
        type: Opaque
        metadata:
          name: proxmox-cloud-controller-manager
          namespace: kube-system
        stringData:
          config.yaml: |
            clusters:
              - url: https://cluster-api-1.exmple.com:8006/api2/json
                insecure: false
                token_id: "kubernetes-csi@pve!ccm"
                token_secret: "secret"
                region: Region-1
    - name: proxmox-csi-plugin
      contents: |-
        apiVersion: v1
        kind: Secret
        type: Opaque
        metadata:
          name: proxmox-csi-plugin
          namespace: csi-proxmox
        stringData:
          config.yaml: |
            clusters:
              - url: https://cluster-api-1.exmple.com:8006/api2/json
                insecure: false
                token_id: "kubernetes-csi@pve!csi"
                token_secret: "secret"
                region: Region-1
  externalCloudProvider:
    enabled: true
    manifests:
      - https://raw.githubusercontent.com/sergelogvinov/proxmox-cloud-controller-manager/main/docs/deploy/cloud-controller-manager.yml
      - https://raw.githubusercontent.com/sergelogvinov/proxmox-csi-plugin/main/docs/deploy/proxmox-csi-plugin.yml
```
