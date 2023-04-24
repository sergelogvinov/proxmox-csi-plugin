# Proxmox CSI Plugin

```shell
kubectl get csistoragecapacities -ocustom-columns=CLASS:.storageClassName,AVAIL:.capacity,ZONE:.nodeTopology.matchLabels -A
```

```shell
kubectl get CSINode worker-11 -oyaml
```