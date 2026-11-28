# Proxmox CSI Plugin

A CSI driver that provisions Kubernetes PersistentVolumes as Proxmox VE disks, plus `pvecsictl`, a CLI that moves and rebinds those volumes.

## Volume operations (pvecsictl)

**Rebind**:
Recreating one or more **Claims** against their existing PersistentVolumes, with a new name, a new topology, or each other's volume, without ever releasing the underlying disk.
_Avoid_: recreate, replace, rebuild

**Hold**:
The state of a **Claim** that is being **Rebound**: its volume is pinned to Retain, its original reclaim policy is recorded, and the Claim is Terminating but kept by a finalizer. No new pod can start with it and no StatefulSet can recreate it, but pods already using it keep it in use until they are gone.
_Avoid_: lock

**Vacate**:
Ending every use of held **Claims**: in force mode deleting the pods that use them, then waiting until Kubernetes no longer protects them, so the **Commit** (or a disk move) can proceed. Nodes are never cordoned; the **Hold** alone keeps new pods from starting.
_Avoid_: drain, evict, release

**Target**:
What a held **Claim** becomes when the **Rebind** is committed: its name, and for a **Migration** the volume's new handle and node topology.

**Commit**:
Finishing a **Rebind** by recreating each held **Claim** as its **Target**.

**Abort**:
Ending a **Hold** by putting the **Claim** back exactly as it was. Possible only before **Commit** has started.
_Avoid_: restore, rollback

**Claim**:
A PersistentVolumeClaim bound to a volume provisioned by this driver.
_Avoid_: PVC (in prose)

**Migration**:
Moving a **Claim**'s disk to another Proxmox node, then **Rebinding** the Claim to the new topology.
_Avoid_: move (for the whole operation)

**Rename**:
A **Rebind** of one **Claim** under a new name.

**Swap**:
A **Rebind** of two **Claims** where each takes the other's volume.
