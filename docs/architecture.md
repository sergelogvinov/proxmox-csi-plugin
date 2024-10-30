# Architecture

Proxmox cluster with local storage like: lvm, lvm-thin, zfs, xfs, ext4, etc.

![ProxmoxClusers!](/docs/proxmox-regions.jpeg)

- Each Proxmox cluster has predefined in cloud-config the region name (see `clusters[].region` below).
- Each Proxmox Cluster has many Proxmox Nodes. In kubernetes scope it is called as `zone`. The name of `zone` is the name of Proxmox node.
- Pods can easily migrate between Kubernetes nodes on the same physical Proxmox node (`zone`).
  The PV will automatically be moved by the CSI Plugin.
- Pods with PVC `cannot` automatically migrate across zones (Proxmox nodes).
  You can manually move PVs across zones using [pvecsictl](docs/pvecsictl.md) to migrate Pods across zones.


```mermaid
---
title: Automatic Pod migration within zone
---
flowchart LR
    subgraph cluster1["Proxmox Cluster (Region 1)"]
        subgraph node11["Proxmox Node (zone 1)"]
                direction BT
                subgraph vm1["VM (worker 1)"]
                        pod11(["Pod (pv-1)"])
                end
                subgraph vm2["VM (worker 2)"]
                        pod12(["Pod (pv-1)"])
                end
                pv11[("Disk (pv-1)")]
        end
        subgraph node12["Proxmox Node (zone 2)"]
                direction BT
                subgraph vm3["VM (worker 3)"]
                    pod22(["Pod (pv-2)"])
                end
            pv22[("Disk (pv-2)")]
        end
    end
pv11 .-> vm1
pv11 -->|automatic| vm2
pod11 -->|migrate| pod12

pv22 --> vm3
```
```mermaid
---
title: Manual migration using pvecsictl across zones
---
flowchart
    subgraph cluster1["Proxmox Cluster (Region 1)"]
    direction BT
        subgraph node11["Proxmox Node (zone 1)"]
                subgraph vm1["VM (worker 1)"]
                        pod11["Pod (pv-1)"]
                end
                subgraph vm2["VM (worker 2)"]
                        pod21["Pod (pv-2)"]
                end
                pv11[("Disk (pv-1)")]
                pv21[("Disk (pv-2)")]
        end
        subgraph node12["Proxmox Node (zone 2)"]
            direction TB
                subgraph vm3["VM (worker 3)"]
                    pod22["Pod (pv-2)"]
                end
            pv22[("Disk (pv-2)")]
        end
    end
pv11 --> vm1
pv21 .-> vm2

pv22 --> vm3
pod21 -->|migrate| pod22
pv21 -->|pvecsictl| pv22
```
