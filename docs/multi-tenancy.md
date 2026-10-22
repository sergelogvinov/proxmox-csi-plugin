# Multi-tenant deployments

Run one Kubernetes cluster per tenant on a shared Proxmox VE cluster, with each
tenant isolated from the others at the Proxmox level.

> Proxmox VE has no native storage multi-tenancy (no per-tenant isolation or quota at
> the storage backend). The model below is assembled by the operator from existing
> building blocks (pools, roles and ACLs, RBD namespaces). A native feature request was
> still open and unanswered in October 2026:
> [bugzilla #7625](https://bugzilla.proxmox.com/show_bug.cgi?id=7625).

The controller does not enforce tenant ownership: a tenant that administers its own
cluster can create a static `PersistentVolume` with any `volumeHandle`, or a `Node` /
`CSINode` carrying any VMID or `providerID`. The controller then calls the Proxmox API
with whatever token it holds. **The security boundary is therefore the Proxmox ACL of
that token, not the CSI driver.** The default single-cluster setup in
[install.md](install.md) grants the role on `/`, which lets any tenant reach every
disk. For multiple tenants, scope each token.

## Model

One controller instance and one Proxmox token per tenant. Each tenant gets its own
Proxmox pool, its own storage entry backed by its own RBD namespace, and a distinct
`controllerVmID`.

| Per tenant | Example for `tenant-a` |
| --- | --- |
| Proxmox pool (holds the tenant's worker VMs and storage) | `tenant-a` |
| RBD namespace (isolates the images) | `<ceph-pool>/tenant-a` |
| Storage entry pointing to that namespace | `tenant-a` |
| User + token, ACL on `/pool/tenant-a` only | `csi-tenant-a@pve!csi` |
| `controllerVmID` (distinct per tenant) | `9001` |

`<ceph-pool>` is the Ceph pool. The Proxmox pool is a separate object, here also named
`tenant-a`. Pick a `controllerVmID` that no VM uses: volumes are named
`vm-<controllerVmID>-pvc-*`, and a real VM with that ID would be seen as their owner. On the
Kubernetes side, the tenant's StorageClass sets `storage: tenant-a`, and the nodes carry the
topology labels described in [install.md](install.md).

## Proxmox setup

```shell
# One RBD namespace per tenant, inside the shared Ceph pool.
rbd namespace create <ceph-pool>/tenant-a

# One storage entry per tenant, bound to that namespace (Ceph hosted on the Proxmox nodes).
pvesm add rbd tenant-a --pool <ceph-pool> --namespace tenant-a --content images --krbd 0
# With an external Ceph cluster, give a Ceph user limited to the namespace instead
# (--keyring is a file on the node where you run pvesm, and Proxmox installs it as
# /etc/pve/priv/ceph/tenant-a.keyring):
#   ceph auth get-or-create client.tenant-a mon 'profile rbd' \
#     osd 'profile rbd pool=<ceph-pool> namespace=tenant-a' > tenant-a.keyring
#   pvesm add rbd tenant-a --pool <ceph-pool> --namespace tenant-a --content images --krbd 0 \
#     --monhost <mon1,mon2,mon3> --username tenant-a --keyring tenant-a.keyring

# One pool per tenant, holding its worker VMs and its storage entry.
pveum pool add tenant-a
pveum pool modify tenant-a --storage tenant-a
pveum pool modify tenant-a --vms <worker-vmids>   # comma-separated VMIDs

# Minimal role. No VM.Allocate: the tenant cannot create or destroy VMs.
# Datastore.Allocate is needed to delete volumes (see "What the ACL protects").
pveum role add CSITENANT --privs "VM.Audit VM.Config.Disk Datastore.Allocate Datastore.AllocateSpace Datastore.Audit"

# User + token, ACL scoped to the tenant's pool only. With privsep=1 the token's
# effective rights are the intersection of the user's and the token's, so the
# backing user needs the role too, not only the token. The token secret is displayed
# once: keep it.
pveum user add csi-tenant-a@pve
pveum user token add csi-tenant-a@pve csi --privsep 1
pveum acl modify /pool/tenant-a --users csi-tenant-a@pve --roles CSITENANT
pveum acl modify /pool/tenant-a --tokens 'csi-tenant-a@pve!csi' --roles CSITENANT
```

Add every new worker VM to the pool (`pveum pool modify tenant-a --vms <vmid>`). A VM outside
the pool is out of the token's reach, so the controller cannot attach disks to it.

On a Ceph cluster hosted on the Proxmox nodes, unless you pass `--keyring`, `pvesm add rbd` installs
a copy of the Ceph admin keyring for every storage entry, even if you give `--monhost` and
`--username`. Proxmox, not Ceph, then keeps the tenants apart. With `--keyring`, Proxmox installs
your file instead, and a Ceph user limited to the namespace (commands above) adds a boundary at the
Ceph level: in the test, such a key could not read another tenant's namespace, while the admin key
could.

This part is for a Helm install, with the `csi-proxmox` namespace created first as described in
[install.md](install.md). Deploy one controller per tenant, each with that tenant's token and
`controllerVmID` in its cloud config. Keep the token out of the Helm values, which are stored
in the Helm release, by putting the cloud config in a Secret that you create yourself:

```yaml
# config.yaml
features:
  controllerVmID: 9001
clusters:
  - url: https://<proxmox>:8006/api2/json
    ca_file: /etc/pve-ca/pve-root-ca.pem
    token_id: "csi-tenant-a@pve!csi"
    token_secret: "<secret>"
    region: <region>
```

`<region>` is the value of the `topology.kubernetes.io/region` label of that tenant's nodes.

```shell
kubectl -n csi-proxmox create secret generic proxmox-csi-config --from-file=config.yaml
kubectl -n csi-proxmox create configmap proxmox-ca --from-file=pve-root-ca.pem=<ca.pem>
```

Delete `config.yaml` once the Secret exists, since it holds the token. Then point the chart at
the Secret and mount the CA in the controller:

```yaml
existingConfigSecret: proxmox-csi-config
existingConfigSecretKey: config.yaml
extraVolumes:
  - name: proxmox-ca
    configMap:
      name: proxmox-ca
extraVolumeMounts:
  - name: proxmox-ca
    mountPath: /etc/pve-ca
    readOnly: true
```

The chart restarts the controller when `config` changes, not when the Secret does. A controller
that is not restarted keeps using the previous configuration, so after you change the Secret,
restart the controller Deployment (`kubectl -n csi-proxmox get deploy` lists it; its name ends
with `-controller`).

The default Proxmox certificate is self-signed, so the controller needs the CA that signed it:
`ca_file` takes a path, or a comma-separated list of paths, to PEM bundles. With the default
`insecure: false` and no CA, the controller's calls to Proxmox fail with
`tls: failed to verify certificate`. Set
`insecure: true` only in a lab. The host in `url` must be one of the names or addresses of the
certificate. Mount the CA outside `/etc/proxmox`, which is already the mount point of the Secret.

## What the ACL protects

With a token scoped to `/pool/tenant-a`, the Proxmox API refuses the cross-tenant actions
tested below. For a **direct Proxmox API request** the refusal is an
HTTP error:

| Direct API request by tenant A | Result |
| --- | --- |
| List / allocate / delete in another tenant's storage | `403` (`Datastore.*`) |
| Attach a disk to another tenant's VM | `403` (`VM.Config.Disk`) |
| Attach another tenant's disk to the tenant's own VM | `403` (no access to the other storage) |
| Create or change a storage entry | `403` (`Datastore.Allocate` is needed on `/storage`) |
| Enumerate another tenant's VMs or storages | not returned by `/cluster/resources` |

The role includes `Datastore.Allocate` because the driver needs it to delete volumes: without it,
deleting a volume answers `403` and the volume stays in the storage. Granted on the pool, it lets
the token delete volumes of its own storage and read that storage's configuration, including the
Ceph pool and the namespace. It does not let the token create, change, delete or scan storages:
that needs it on `/storage`.

Through the **CSI driver** the same ACL applies, but the answer depends on the volume ID. A
`DeleteVolume` for an out-of-scope volume of a shared storage, whose volume ID has an empty zone
(the form the driver gives to every shared-storage volume), returns success without deleting
anything: the storage is invisible to the token, so the controller treats the volume as already
gone. A volume ID that carries a zone, which is a node name, fails instead: the driver answers
`Internal` with the `403` of the other storage (`Permission check failed (/storage/tenant-b,
Datastore.Audit|Datastore.AllocateSpace)`), nothing is deleted, and the provisioner keeps
retrying, so the PersistentVolume stays `Released`. In both cases the other tenant's volume is
untouched.

## Verify the isolation

Run these from a bash shell on any host that can reach the Proxmox API, using **tenant A's
token**. The helper below reads the token without echoing it and hands it to `curl` through a
file descriptor, so it stays out of the process list and out of your shell history. Do not put
the token in a double-quoted assignment: in an interactive shell the `!` of the token ID
triggers history expansion. The helper was tested with curl 8.14; `-H @file` needs a recent curl.

```shell
API=https://<proxmox>:8006/api2/json
CA=<ca.pem>   # /etc/pve/pve-root-ca.pem on a node for the default self-signed certificate
read -rs -p 'tenant A token (csi-tenant-a@pve!csi=<secret>): ' T; echo
api() { curl -s --cacert "$CA" -H @<(printf 'Authorization: PVEAPIToken=%s\n' "$T") "$@"; }
```

In the commands below, `<node>` is any node for storage requests, `<a-vmid>` and `<a-vm-node>`
are a disposable VM of tenant A (a test VM, or a drained worker) and the node it runs on,
`<tenant-b-vmid>` and `<tenant-b-vm-node>` are a disposable VM of tenant B (or any VM outside
tenant A's pool) and its node, `<b-controller-vmid>` is tenant B's `controllerVmID`, and
`<free-id>` is an unused VMID. An attach request replaces whatever is in its slot, so `scsi9` and
`scsi30` must be free on both VMs: check with `GET .../qemu/<vmid>/config`, or pick other free slots.

Each direct cross-tenant request must return `403`. The enumeration requests return `200`
with only tenant A's entries, so check that no tenant B entry appears in them. The own-pool
requests must succeed. Use throwaway volume names you created for the test, never a real
disk. Only `403` proves a denial: `000` is a connection or TLS error, `401` an invalid token,
and `404` or `500` usually a wrong storage, VM or node name. The tenant B storage must exist
and be active on `<node>`. A request that must fail but returns `200` means the isolation is
broken: delete what it created (the volume in tenant B's storage, the VM, or the disk entry added to a
VM) and fix the ACL
before going on.

```shell
# Enumeration: tenant A sees only its own storages and VMs.
api "$API/cluster/resources?type=storage"   # only tenant-a entries
api "$API/cluster/resources?type=vm"        # only tenant A's VMs

# Read another tenant's storage -> 403.
api -o /dev/null -w '%{http_code}\n' "$API/nodes/<node>/storage/tenant-b/status"

# Allocate in another tenant's storage -> 403.
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<node>/storage/tenant-b/content" --data-urlencode filename=vm-<b-controller-vmid>-pvc-x --data-urlencode size=1G --data-urlencode vmid=<b-controller-vmid>

# Create a VM -> 403 (the role has no VM.Allocate). `GET $API/cluster/nextid` gives a free <free-id>.
api -o /dev/null -w '%{http_code}\n' -X POST "$API/nodes/<node>/qemu" --data-urlencode vmid=<free-id> --data-urlencode memory=512

# Positive controls inside tenant A's own pool -> 200.
# Visibility (Datastore.Audit):
api -o /dev/null -w '%{http_code}\n' "$API/nodes/<node>/storage/tenant-a/status"
# Allocation (Datastore.AllocateSpace), then removal of the throwaway volume (Datastore.Allocate):
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<node>/storage/tenant-a/content" --data-urlencode filename=vm-<a-vmid>-isolation-check --data-urlencode size=1G --data-urlencode vmid=<a-vmid>
api -o /dev/null -w '%{http_code}\n' -X DELETE \
  "$API/nodes/<node>/storage/tenant-a/content/tenant-a:vm-<a-vmid>-isolation-check"
# Attachment (VM.Config.Disk): allocate a throwaway disk and attach it to tenant A's VM (slot scsi9).
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<node>/storage/tenant-a/content" --data-urlencode filename=vm-<a-vmid>-attach-check --data-urlencode size=1G --data-urlencode vmid=<a-vmid>
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<a-vm-node>/qemu/<a-vmid>/config" --data-urlencode scsi9=tenant-a:vm-<a-vmid>-attach-check

# Clean up: detach the disk, then find the `unused<N>` entry that points to
# vm-<a-vmid>-attach-check in the VM config and remove it, which also removes the volume.
api -o /dev/null -w '%{http_code}\n' -X PUT "$API/nodes/<a-vm-node>/qemu/<a-vmid>/config" --data-urlencode delete=scsi9
api "$API/nodes/<a-vm-node>/qemu/<a-vmid>/config"
api -o /dev/null -w '%{http_code}\n' -X PUT "$API/nodes/<a-vm-node>/qemu/<a-vmid>/config" --data-urlencode delete=unused<N>

# Attach a disk to another tenant's VM -> 403, with a disk that is not attached anywhere (slot scsi30).
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<node>/storage/tenant-a/content" --data-urlencode filename=vm-<a-vmid>-deny-check --data-urlencode size=1G --data-urlencode vmid=<a-vmid>
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<tenant-b-vm-node>/qemu/<tenant-b-vmid>/config" --data-urlencode scsi30=tenant-a:vm-<a-vmid>-deny-check
api -o /dev/null -w '%{http_code}\n' -X DELETE \
  "$API/nodes/<node>/storage/tenant-a/content/tenant-a:vm-<a-vmid>-deny-check"

# Attach another tenant's disk to tenant A's VM -> 403 (the disk name does not need to exist).
# If it returns 200, remove the scsi30 entry it added (the slot was free, so nothing was replaced).
api -o /dev/null -w '%{http_code}\n' -X POST \
  "$API/nodes/<a-vm-node>/qemu/<a-vmid>/config" --data-urlencode scsi30=tenant-b:vm-<b-controller-vmid>-check
```

Do not remove the throwaway volume through the storage endpoint instead of `delete=unused<N>`:
the volume would go but the VM would keep a dangling `unused<N>` entry that the tenant cannot
remove. When you are done, run `unset T; unset -f api` so the token does not stay in the shell.

For a throwaway lab you can replace `--cacert "$CA"` with `-k` in the helper, which disables TLS
certificate verification. Do not do that outside a lab: the token would be sent over an
unverified connection.

The positive controls matter: without them, a `403` could be a plain "not found" from
ACL-filtered enumeration, or the token could lack a privilege such as `VM.Config.Disk`
altogether, rather than a real cross-tenant denial.

## Pitfalls

- **One storage entry per RBD namespace.** Two storage entries pointing to the same
  pool and namespace expose the same images: a tenant with rights on its own entry
  could then list, attach and delete another tenant's volumes through it. Give each
  storage entry its own namespace (or pool).
- **Do not grant the role on `/`.** That is the single-cluster default and gives every
  tenant access to every disk. Scope each token to its pool.
- **Distinct `controllerVmID` per tenant**, so volumes (`vm-<controllerVmID>-pvc-*`) are named
  distinctly and attributable to a tenant. This is a naming convention, not an access
  control: the boundary is the ACL and the storage/namespace separation above, and a
  shared namespace or ACL gives cross-tenant access whatever the VMIDs are.

## Limits

Checked on Proxmox VE 9.2 with Ceph 19.2.6.

- **No volume snapshots.** The driver does not support snapshots on RBD or on any other
  shared storage: `CreateSnapshot` answers `storage type rbd(ceph) does not support
  snapshot`. On other storages the feature needs `root@pam` (see
  [volumesnapshot.md](volumesnapshot.md)), and the Proxmox copy endpoint it relies on answers
  `403` to any other user, so it cannot work with a token scoped to a tenant. Keep
  `controller.snapshotter.enabled: false`.
- **No PVC cloning on shared storage.** A `copy` inside the same shared storage
  finishes without error but creates no volume (`PVE::Storage::storage_migrate`
  returns early). The driver refuses such a request with `InvalidArgument`.
- **No per-tenant quota.** An RBD namespace has no quota (`rbd namespace` only offers
  `create`, `list` and `remove`), and Ceph quotas apply to a whole pool. Proxmox does
  not cap the size either: a tenant token allocated a 1 TiB thin image (`size=1024G` in
  the allocation request) in a pool with 182 GiB available. A tenant can therefore fill
  the shared pool; monitor usage per namespace and alert on pool fullness.

## Scale

Each tenant adds one storage entry, and `pvestatd` polls every active storage on every
node. Measured twice on a 3-node cluster (AMD EPYC 7313P, 31 GB of RAM each, 9 OSDs in total),
with one RBD namespace per entry, no I/O, and the timings taken on a single node:

| Tenant entries added | `storage.cfg` | `pvesm status` | `pvestatd` update cycle |
| --- | --- | --- | --- |
| 0 | 605 B | 0.7 s | not logged (fast) |
| 150 | 11.6 KB | 5.2 - 5.4 s | 5.1 - 6.0 s |
| 300 | 22.5 KB | 9.2 - 9.4 s | 9.7 - 10.2 s |

- **File size is not the limit.** An entry costs about 73 bytes. `storage.cfg` is a single
  file and pmxcfs limits a file to 1 MiB (a write of 1,048,577 bytes was refused with `File too
  large`), a size that would be reached at roughly 14,000 entries (extrapolated, not reached).
  The whole `/etc/pve` filesystem has a separate limit of 128 MiB (`MEMDB_MAX_FILE_SIZE` and
  `MEMDB_MAX_FSSIZE` in pve-cluster).
- **`pvestatd` is.** A cycle grows by about 30 ms per entry and the loop runs every
  10 s. At 300 entries a cycle takes 9.7 to 10.2 s, which is the length of the loop, so
  on this hardware status updates start to fall behind there. Nothing was tested past 300.
- `/cluster/resources` stayed near 1 s throughout.

Not measured: the effect on the other nodes, on the Ceph cluster under real I/O, and on
the GUI. Treat these figures as an order of magnitude and measure on your own cluster
before scaling the tenant count.
