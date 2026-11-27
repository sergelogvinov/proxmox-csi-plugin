# Proxmox CSI Plugin

A CSI driver that provisions Kubernetes PersistentVolumes as Proxmox VE disks, plus `pvecsictl`, a CLI that moves and rebinds those volumes.

## Volume operations (pvecsictl)

**Rebind**:
Recreating one or more **Claims** against their existing PersistentVolumes, with a new name, a new topology, or each other's volume, without ever releasing the underlying disk.
_Avoid_: recreate, replace, rebuild

**Hold**:
The state of a **Claim** that is being **Rebound**: its volume is pinned to Retain, its original reclaim policy is recorded, and the Claim is Terminating but kept by a finalizer. No new pod can start with it and no StatefulSet can recreate it, but pods already using it keep it in use until they are gone. A **Drain** follows it.
_Avoid_: lock

**Drain**:
Waiting until no pod can use a held **Claim** anymore (Kubernetes has released `kubernetes.io/pvc-protection`), deleting the pods that use it in force mode. Required before **Commit**. The StatefulSets of the drained pods are triggered after **Commit** or **Abort**, so they recreate their pods without waiting for their backoff.
_Avoid_: evict, wait unused

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
