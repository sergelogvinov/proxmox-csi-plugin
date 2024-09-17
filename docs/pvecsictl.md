# pvecsictl tool

`pvecsictl` is a command line tool for managing the Proxmox CSI PV/PVC resources.

**Warning**: This tool is under development and should be used with caution.
The commands and flags may change in the future.

## Installation

It works on macOS (Intel/ARM) and Linux (amd64/arm64)

```shell
brew install sergelogvinov/tap/pvecsictl
```

RBAC permissions required for the tool

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pvecsictl
rules:
  # Get list of pods with PVCs
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch", "delete"]
  - apiGroups: ["storage.k8s.io"]
    resources: ["csinodes"]
    verbs: ["get", "list"]
  # Create and delete PV/PVC
  - apiGroups: [""]
    resources: ["persistentvolumes"]
    verbs: ["get", "list", "watch", "create", "patch", "delete"]
  - apiGroups: [""]
    resources: ["persistentvolumeclaims"]
    verbs: ["get", "list", "watch", "create", "patch", "delete"]
  # Node cordoning/uncordoning
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get", "list", "watch", "patch"]
```

## Usage

```shell
Usage:
  pvecsictl [command]

Available Commands:
  migrate     Migrate data from one Proxmox node to another
  rename      Rename PersistentVolumeClaim
  swap        Swap PersistentVolumes between two PersistentVolumeClaims
```

## Commands

### Migrate

Migration requires root privileges on the Proxmox cluster.
You need to provide the cloud-config file with root credentials (username/password) to the Proxmox cluster.

```yaml
clusters:
  - url: https://cluster-1:8006/api2/json
    username: root@pam
    password: "strong-password"
    ...
```

To migrate the data for PVC `storage-test-0` first find the backing PV by running

```shell
kubectl -n default get pvc storage-test-0 -ojsonpath='{.spec.volumeName}'
```

which in our case is `pvc-0d79713b-6d0b-41e5-b387-42af370d083f`.

Next find the PV topology by inspecting its `nodeAffinity`

```shell
kubectl -n default get pv pvc-0d79713b-6d0b-41e5-b387-42af370d083f -ojsonpath='{.spec.nodeAffinity}'
```

which gives us

```json
{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"topology.kubernetes.io/region","operator":"In","values":["fsn1"]},{"key":"topology.kubernetes.io/zone","operator":"In","values":["hvm-1"]}]}]}}
```

By looking at the above `topology.kubernetes.io` fields we see that the PV is located in zone (node) `hvm-1` in region (cluster) `fsn1`.

To move the PVC from zone `hvm-1` to `hvm-2` we can run

```shell
pvecsictl migrate --config=hack/cloud-config.yaml -n default storage-test-0 hvm-2
````

If you're met with

```shell
ERROR Error: persistentvolumeclaims is using by pods: test-0 on node kube-store-11, cannot move volume
```

you can force the process by adding the `--force` flag

```shell
pvecsictl migrate --config=hack/cloud-config.yaml -n default storage-test-0 hvm-2 --force

INFO persistentvolumeclaims is using by pods: test-0 on node kube-store-11, trying to force migration
INFO cordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO terminated pods: test-0
INFO waiting pods: test-0
...
INFO waiting pods: test-0
INFO moving disk vm-9999-pvc-0d79713b-6d0b-41e5-b387-42af370d083f to proxmox node hvm-2
INFO replacing persistentvolume topology
INFO uncordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO persistentvolumeclaims storage-test-0 has been migrated to proxmox node hvm-2
```

To check that the zone has changed run
```shell
kubectl -n default get pv pvc-0d79713b-6d0b-41e5-b387-42af370d083f -ojsonpath='{.spec.nodeAffinity}'
```

again

```json
{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"topology.kubernetes.io/region","operator":"In","values":["fsn1"]},{"key":"topology.kubernetes.io/zone","operator":"In","values":["hvm-2"]}]}]}}
```

to verify that the zone is now `hvm-2`.

Pod lifetime when running `pvecsictl` with the `--force` flag

```shell
# kubectl -n default get pods -owide -w
test-0   1/1     Running            0          4m28s   10.32.19.119   kube-store-11   <none>           <none>
test-0   1/1     Terminating        0          6m44s   10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Terminating        0          7m      <none>         kube-store-11   <none>           <none>
test-0   0/1     Terminating        0          7m      10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Terminating        0          7m      10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Terminating        0          7m      10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Pending            0          0s      <none>         <none>          <none>           <none>
test-0   0/1     Pending            0          0s      <none>         <none>          <none>           <none>
test-0   0/1     Pending            0          62s     <none>         <none>          <none>           <none>
test-0   0/1     Pending            0          71s     <none>         kube-21         <none>           <none>
test-0   0/1     ContainerCreating  0          71s     <none>         kube-21         <none>           <none>
test-0   1/1     Running            0          85s     10.32.11.96    kube-21         <none>           <none>
```

Here we've migrated the StatefulSet Pod with PVC to another node.
Force mode helps to migrate StatefulSet deployment to another node without scaling down all replicas.
It cordoned all nodes which have csi-proxmox plugin. Migrated the disk to another node and un-cordoned all nodes.

### Rename

Rename PersistentVolumeClaim.

Check the current PVCs:

```shell
# kubectl -n default get pvc
storage-test-0   Bound    pvc-0d79713b-6d0b-41e5-b387-42af370d083f   5Gi        RWO            proxmox-xfs    <unset>                 7m6s
storage-test-1   Bound    pvc-2727795f-680c-410a-b130-2e5dc85efcb3   5Gi        RWO            proxmox-xfs    <unset>                 15m
```

Rename the `storage-test-0` PVC by running

```shell
pvecsictl rename -n default storage-test-0 storage-test-2
```

If you're met with

```shell
ERROR Error: persistentvolumeclaims is using by pods: test-0 on node kube-21, cannot move volume
```

You can force the process by adding the `--force` flag

```shell
pvecsictl rename -n default storage-test-0 storage-test-2 --force

INFO persistentvolumeclaims is using by pods: test-0 on node kube-21, trying to force migration
INFO cordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO terminated pods: test-0
INFO waiting pods: test-0
...
INFO waiting pods: test-0
INFO uncordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO persistentvolumeclaims storage-test-0 has been renamed
```

Check the result:

* storage-test-0 -> storage-test-2
* storage-test-0 has new PV

```shell
# kubectl -n default get pvc
storage-test-0   Bound    pvc-2c6a06b2-e693-4807-a872-63ca67e6ee52   5Gi        RWO            proxmox-xfs    <unset>                 100s
storage-test-1   Bound    pvc-2727795f-680c-410a-b130-2e5dc85efcb3   5Gi        RWO            proxmox-xfs    <unset>                 19m
storage-test-2   Bound    pvc-0d79713b-6d0b-41e5-b387-42af370d083f   5Gi        RWO            proxmox-xfs    <unset>                 102s
```

Pod lifetime during rename

```shell
# kubectl -n default get pods -owide -w
test-0   1/1     Running             0          9m37s   10.32.11.96   kube-21         <none>           <none>
test-1   1/1     Running             0          16m     10.32.4.232   kube-store-21   <none>           <none>
test-0   1/1     Terminating         0          10m     10.32.11.96   kube-21         <none>           <none>
test-0   0/1     Terminating         0          11m     <none>        kube-21         <none>           <none>
test-0   0/1     Terminating         0          11m     10.32.11.96   kube-21         <none>           <none>
test-0   0/1     Terminating         0          11m     10.32.11.96   kube-21         <none>           <none>
test-0   0/1     Terminating         0          11m     10.32.11.96   kube-21         <none>           <none>
test-0   0/1     Pending             0          0s      <none>        <none>          <none>           <none>
test-0   0/1     Pending             0          0s      <none>        <none>          <none>           <none>
test-0   0/1     Pending             0          8s      <none>        <none>          <none>           <none>
test-0   0/1     Pending             0          13s     <none>        kube-store-11   <none>           <none>
test-0   0/1     ContainerCreating   0          13s     <none>        kube-store-11   <none>           <none>
test-0   1/1     Running             0          24s     10.32.19.17   kube-store-11   <none>           <none>
```

### Swap

Swap PersistentVolumeClaim between two PVCs.

Check the current PVC:

```shell
# kubectl get pods,pvc
NAME         READY   STATUS    RESTARTS   AGE
pod/test-0   1/1     Running   0          2m58s
pod/test-1   1/1     Running   0          2m58s

NAME                                   STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   VOLUMEATTRIBUTESCLASS   AGE
persistentvolumeclaim/storage-test-0   Bound    pvc-e248bc56-dcf4-4145-93b9-a374a7c3b900   10Gi       RWO            proxmox-lvm    <unset>                 2m51s
persistentvolumeclaim/storage-test-1   Bound    pvc-41b7078d-aa9f-4757-9056-8bd1e8e0697f   15Gi       RWO            proxmox-lvm    <unset>                 2m52s
```

Swap PVCs:

```shell
pvecsictl swap -n default storage-test-0 storage-test-1 -f

INFO persistentvolumeclaims is using by pods: test-0 on node builder-03a, trying to force swap
INFO persistentvolumeclaims is using by pods: test-1 on node builder-04b, trying to force swap
INFO cordoned nodes: builder-03a,builder-03b,builder-04a,builder-04b
INFO terminated pods: test-0,test-1
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO persistentvolumeclaims storage-test-0,storage-test-1 has been swapped
INFO uncordoning nodes: builder-03a,builder-03b,builder-04a,builder-04b
```

Check the result:

* storage-test-0 <-> storage-test-1

```shell
# kubectl get pods,pvc
NAME         READY   STATUS    RESTARTS   AGE
pod/test-0   1/1     Running   0          19s
pod/test-1   1/1     Running   0          19s

NAME                                   STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   VOLUMEATTRIBUTESCLASS   AGE
persistentvolumeclaim/storage-test-0   Bound    pvc-41b7078d-aa9f-4757-9056-8bd1e8e0697f   15Gi       RWO            proxmox-lvm    <unset>                 13s
persistentvolumeclaim/storage-test-1   Bound    pvc-e248bc56-dcf4-4145-93b9-a374a7c3b900   10Gi       RWO            proxmox-lvm    <unset>                 13s
```

# Feedback

Use the [GitHub discussions](https://github.com/sergelogvinov/proxmox-csi-plugin/discussions) for feedback and questions.
