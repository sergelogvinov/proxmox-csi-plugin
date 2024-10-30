direction down
styleMode: plain

CLUSTER1 [label: "Proxmox Cluster (Region-1)"] {
  Node 1 [label: "Proxmox Node 1 (zone 1)"] {
    VM1 [label: "VM-1"] {
      pod2 [icon: k8s-pod, label: "pod", colorMode: outline, color: black] {
      pvc1 [icon: k8s-pvc, label: "pvc", colorMode: outline, color: black]}
    }
    VM2 [label: "VM-2"] {
      pod3 [icon: k8s-pod, label: "pod", colorMode: outline, color: black]  {
      pvc3 [icon: k8s-pvc, label: "pvc", colorMode: outline, color: black]}
    }
    DiskNode1 [icon:azure-disks, label:"Local Disk"]
  }
  Node 2 [label: "Proxmox Node 2 (zone 2)"] {
    VM3 [label: "VM-3"] {
      pod4 [icon: k8s-pod, label: "pod", colorMode: bold] {
      pvc4 [icon: k8s-pvc, label: "pvc", colorMode: bold]
      }
    }
    DiskNode2 [icon:azure-disks, label:"Local Disk"]
  }
  DiskSH1 [icon:azure-disk-pool, label:"Shared Disk"]
}

pvc1 -- DiskNode1
pvc3 -- DiskNode1
pvc4 -- DiskNode2
pvc1 -- DiskSH1
pvc3 -- DiskSH1
pvc4 - DiskSH1
