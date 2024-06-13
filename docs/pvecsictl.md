# pvecsictl tool

`pvecsictl` is a command line tool for managing the Proxmox CSI PV/PVC resources.

**Warning**: This tool is under development and should be used with caution.
The commands and  flags may change in the future.

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

Migration requires the root privileges on the Proxmox cluster.
You need to provide the cloud-config file with the root credentials (username/password) to the Proxmox cluster.

```yaml
clusters:
  - url: https://cluster-1:8006/api2/json
    username: root@pam
    password: "strong-password"
    ...
```

Migrate data from one Proxmox node to another.

```shell
# kubectl -n default get pvc storage-test-0 -ojsonpath='{.spec.volumeName}'
pvc-0d79713b-6d0b-41e5-b387-42af370d083f

# kubectl -n default get pv pvc-0d79713b-6d0b-41e5-b387-42af370d083f -ojsonpath='{.spec.nodeAffinity}'
{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"topology.kubernetes.io/region","operator":"In","values":["fsn1"]},{"key":"topology.kubernetes.io/zone","operator":"In","values":["hvm-1"]}]}]}}
```

It has zone `hvm-1` and region `fsn1` affinity.
Now we want to move it to another node:

```shell
pvecsictl migrate --config=hack/cloud-config.yaml -n default storage-test-0 hvm-2

ERROR Error: persistentvolumeclaims is using by pods: test-0 on node kube-store-11, cannot move volume

# Force process
pvecsictl migrate --config=hack/cloud-config.yaml -n default storage-test-0 hvm-2 --force

INFO persistentvolumeclaims is using by pods: test-0 on node kube-store-11, trying to force migration
INFO cordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO terminated pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO moving disk vm-9999-pvc-0d79713b-6d0b-41e5-b387-42af370d083f to proxmox node hvm-2
INFO replacing persistentvolume topology
INFO uncordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO persistentvolumeclaims storage-test-0 has been migrated to proxmox node hvm-2
```

Check the result:

`topology.kubernetes.io/zone` was changed from hvm-1 to hvm-2

```shell
# kubectl -n default get pv pvc-0d79713b-6d0b-41e5-b387-42af370d083f -ojsonpath='{.spec.nodeAffinity}'
{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"topology.kubernetes.io/region","operator":"In","values":["fsn1"]},{"key":"topology.kubernetes.io/zone","operator":"In","values":["hvm-2"]}]}]}}
```

What was with the pod (force mode):

```shell
# kubectl -n default get pods -owide -w
test-0   1/1     Running   0          4m28s   10.32.19.119   kube-store-11   <none>           <none>
test-1   1/1     Running   0          4m28s   10.32.4.232    kube-store-21   <none>           <none>
test-0   1/1     Terminating   0          6m44s   10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Terminating   0          7m      <none>         kube-store-11   <none>           <none>
test-0   0/1     Terminating   0          7m      10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Terminating   0          7m      10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Terminating   0          7m      10.32.19.119   kube-store-11   <none>           <none>
test-0   0/1     Pending       0          0s      <none>         <none>          <none>           <none>
test-0   0/1     Pending       0          0s      <none>         <none>          <none>           <none>
test-0   0/1     Pending       0          62s     <none>         <none>          <none>           <none>
test-0   0/1     Pending       0          71s     <none>         kube-21         <none>           <none>
test-0   0/1     ContainerCreating   0          71s     <none>         kube-21         <none>           <none>
test-0   1/1     Running             0          85s     10.32.11.96    kube-21         <none>           <none>
```

So we migrated the Statefulset pod with PVC to another node.
Force mode helps to migrate statefulset deployment to another node without scaling down all replicas.
It cordoned all nodes which have csi-proxmox plugin. Migrated the disk to another node and uncordoned all nodes.

### Rename

Rename PersistentVolumeClaim.

Check the current PVC:

```shell
# kubectl -n default get pvc
storage-test-0   Bound    pvc-0d79713b-6d0b-41e5-b387-42af370d083f   5Gi        RWO            proxmox-xfs    <unset>                 7m6s
storage-test-1   Bound    pvc-2727795f-680c-410a-b130-2e5dc85efcb3   5Gi        RWO            proxmox-xfs    <unset>                 15m
```

Rename one of them:

```shell
pvecsictl rename -n default storage-test-0 storage-test-2

ERROR Error: persistentvolumeclaims is using by pods: test-0 on node kube-21, cannot move volume

# Force process
pvecsictl rename -n default storage-test-0 storage-test-2 --force

INFO persistentvolumeclaims is using by pods: test-0 on node kube-21, trying to force migration
INFO cordoning nodes: kube-11,kube-12,kube-21,kube-22,kube-store-11,kube-store-21
INFO terminated pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
INFO waiting pods: test-0
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

What was with the pod:

```shell
# kubectl -n default get pods -owide -w
test-0   1/1     Running   0          9m37s   10.32.11.96   kube-21       <none>           <none>
test-1   1/1     Running   0          16m     10.32.4.232   kube-store-21 <none>           <none>
test-0   1/1     Terminating   0          10m     10.32.11.96   kube-21      <none>           <none>
test-0   0/1     Terminating   0          11m     <none>        kube-21      <none>           <none>
test-0   0/1     Terminating   0          11m     10.32.11.96   kube-21      <none>           <none>
test-0   0/1     Terminating   0          11m     10.32.11.96   kube-21      <none>           <none>
test-0   0/1     Terminating   0          11m     10.32.11.96   kube-21      <none>           <none>
test-0   0/1     Pending       0          0s      <none>        <none>       <none>           <none>
test-0   0/1     Pending       0          0s      <none>        <none>       <none>           <none>
test-0   0/1     Pending       0          8s      <none>        <none>       <none>           <none>
test-0   0/1     Pending       0          13s     <none>        kube-store-11   <none>           <none>
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

Use the [github discussions](https://github.com/sergelogvinov/proxmox-csi-plugin/discussions) for feedback and questions.
