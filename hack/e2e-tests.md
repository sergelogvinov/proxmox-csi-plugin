# Integration tests

## Manual integration tests

### Encrypted PVs

Create PV secret and deploy a pod that uses it.

```yaml
---
apiVersion: v1
data:
  # echo 1f03928033dda2e4fd347e44266cfbc | base64
  encryption-passphrase: MWYwMzkyODAzM2RkYTJlNGZkMzQ3ZTQ0MjY2Y2ZiYw==
kind: Secret
metadata:
  creationTimestamp: null
  name: proxmox-csi-secret
  namespace: kube-system
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: proxmox-secret
parameters:
  csi.storage.k8s.io/fstype: xfs
  csi.storage.k8s.io/node-stage-secret-name: "proxmox-csi-secret"
  csi.storage.k8s.io/node-stage-secret-namespace: "kube-system"
  csi.storage.k8s.io/node-expand-secret-name: "proxmox-csi-secret"
  csi.storage.k8s.io/node-expand-secret-namespace: "kube-system"
  storage: lvm
provisioner: csi.proxmox.sinextra.dev
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: test
  namespace: default
  labels:
    app: alpine
spec:
  podManagementPolicy: Parallel
  serviceName: test
  replicas: 1
  template:
    metadata:
      labels:
        app: alpine
    spec:
      terminationGracePeriodSeconds: 3
      tolerations:
        - effect: NoSchedule
          key: node-role.kubernetes.io/control-plane
      nodeSelector:
        # kubernetes.io/hostname: kube-store-02a
        # topology.kubernetes.io/zone: hvm-1
      containers:
        - name: alpine
          image: alpine
          command: ["sleep","1d"]
          securityContext:
            seccompProfile:
              type: RuntimeDefault
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: storage
              mountPath: /mnt
  updateStrategy:
    type: RollingUpdate
  selector:
    matchLabels:
      app: alpine
  volumeClaimTemplates:
    - metadata:
        name: storage
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
        storageClassName: proxmox-secret
```

Run the statefulset, wait for it to be running and exec into the proxmox-csi-plugin-node pod to check the passphrase.

```bash
echo -n "1f03928033dda2e4fd347e44266cfbc" | kube -n csi-proxmox exec -ti proxmox-csi-plugin-node-srm6v -- /sbin/cryptsetup luksOpen --debug --test-passphrase -v /dev/sdb --key-file=-
```
