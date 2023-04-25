# Proxmox CSI Plugin

```shell
kubectl get csistoragecapacities -ocustom-columns=CLASS:.storageClassName,AVAIL:.capacity,ZONE:.nodeTopology.matchLabels -A
```

```shell
kubectl get CSINode worker-11 -oyaml
```

* https://arslan.io/2018/06/21/how-to-write-a-container-storage-interface-csi-plugin/
* https://kubernetes-csi.github.io/docs/

* https://pve.proxmox.com/wiki/Performance_Tweaks
