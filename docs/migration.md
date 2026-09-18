# Migration: `luthermonson/go-proxmox` → `sergelogvinov/go-proxmox-rest`

Status: **in progress** — §4 (the `go-proxmox-rest` gaps) is done; §5/§6 (the plugin-side
`pkg/proxmoxrest` adapter and call-site migration) has not started.
Owner: platform
Related repos: `sergelogvinov/go-proxmox` (thin wrapper, to be retired), `sergelogvinov/go-proxmox-rest`
(target — pushed to `origin/main` on GitHub, no tagged release yet)

## 1. Why

Today the plugin depends on two Proxmox client libraries at once:

- `github.com/luthermonson/go-proxmox` — the actual HTTP client. Used directly in
  `pkg/csi/utils.go`, `pkg/csi/utils_test.go`, `pkg/proxmoxpool/pool.go`,
  `pkg/tools/proxmox/volume.go` and `test/cluster/cluster.go` for its types
  (`proxmox.ClusterResource`, `proxmox.VirtualMachineConfig`, `proxmox.UPID`, `proxmox.Task`, ...)
  and for low-level escape hatches (`cl.Client.Post`, `cl.Client.Get`, `cl.Client.Delete`).
- `github.com/sergelogvinov/go-proxmox` (`goproxmox.APIClient`) — a hand-rolled convenience
  wrapper *around* `luthermonson/go-proxmox` (see `client.go`, `cluster.go`, `node.go`,
  `storage.go`, `virtual_machine*.go` in that module). Every call site in the plugin actually
  goes through this wrapper; `pkg/csi/controller.go` and `cmd/pvecsictl/clean.go` only ever see
  `*goproxmox.APIClient` and never import `luthermonson/go-proxmox` themselves.

`github.com/sergelogvinov/go-proxmox-rest` is a from-scratch, typed, resource-chaining REST
client (`c.Cluster()...`, `c.Nodes(node).Qemu()...`, `c.Storage()...`) that already covers
essentially the entire surface the plugin uses, plus:

- an in-memory `fakeapi` package (httptest-backed) that can replace the current
  `httpmock` + hand-built `luthermonson` fixtures in `test/cluster/cluster.go`,
- first-class `IsNotFound` / `IsRateLimited` / `IsUnexpected` error helpers instead of
  string-matching (`strings.Contains(err.Error(), "No such storage")`, `"no such job"`, ...),
- no third-party HTTP client dependency chain beyond `resty.dev/v3`.

Migrating removes one full dependency layer (the `sergelogvinov/go-proxmox` wrapper disappears
entirely — it was only ever a stand-in for what `go-proxmox-rest` now provides natively) and
drops the `luthermonson/go-proxmox` transitive dependency.

## 2. Constraints

- **Both client stacks must keep working simultaneously** for the whole migration window — this
  is not a big-bang swap. CI (`make lint`, `make unit`, `make conformance`) must stay green after
  every commit.
- `go-proxmox-rest` lives in a separate repo (`github.com/sergelogvinov/go-proxmox-rest`),
  pushed to `origin/main` but not yet tagged. Until a tag exists, pin it with a pseudo-version
  (`go get github.com/sergelogvinov/go-proxmox-rest@main`) or a local `replace` directive during
  day-to-day development, the same pattern already commented out in `go.mod` for the other two
  modules.
- ~~Three gaps exist in `go-proxmox-rest` today (see §4) and must be closed there first~~ — **done**,
  see §4: all three (VM creation, volume copy/move, blocking task-wait) have shipped in that repo.

## 3. Call-site inventory and target mapping

All current usage goes through `goproxmox.APIClient` (embeds `*luthermonson/go-proxmox.Client`).
Two call sites reach through the embedded client directly instead of a wrapper method —
`cl.Client.ClusterStorage(...)` (`pkg/csi/controller.go:254,804`) bypasses `cl.GetClusterStorage`
entirely and hits a *different* Proxmox endpoint (`/storage/{storage}` config vs.
`/cluster/resources?type=storage`); flag this inconsistency for cleanup during the migration
rather than carrying it forward.

| Current call (`goproxmox.APIClient` / raw `cl.Client.*`) | `go-proxmox-rest` equivalent | Notes |
|---|---|---|
| `NewAPIClient(url, opts...)` | `proxmox.New(cfg, opts...)` | Options are renamed (`WithCredentials`→`WithPasswordAuth`, `WithAPIToken`→`WithTokenAuth`, `WithHTTPClient`→`WithInsecure`/`WithProxy`/transport-level options). |
| `pxClient.Version(ctx)` | `c.Version(ctx)` | 1:1. |
| `pxClient.Cluster(ctx)` + `pxCluster.Resources(ctx, "vm")` | `c.Cluster().Resources().List(ctx, cluster.ListFilter{Type: cluster.ResourceTypeVM})` | 1:1, filter is richer (client-side `Match`, `SkipTemplates`, `VMID`, `Node`). |
| `cl.GetNodeList(ctx)` | `c.Cluster().Resources().List(ctx, ListFilter{Type: ResourceTypeNode})` → map `.Name`/`.Node` | |
| `cl.GetNodeByName(ctx, name)` | same, with `Match` filter on `Node == name`; `ErrNodeNotFound` on empty result | |
| `cl.GetVMByID` / `GetVMByFilter` / `GetVMsByFilter` / `GetVMTemplateByID` / `GetVMTemplatesByFilter` | `c.Cluster().Resources().List(ctx, ListFilter{Type: ResourceTypeVM, GuestType: "qemu", SkipTemplates: ..., VMID: ..., Match: ...})` | `Resource.VMID` is `int`, not `uint64` — ripples through call sites that do `uint64(vmID)` conversions. |
| `cl.GetVMConfig(ctx, vmID)` | `c.Nodes(node).Qemu().Status(ctx, vmid)` (for status/unknown check) + `c.Nodes(node).Qemu().Config(ctx, vmid, nil)` | Two calls, same as today's `GetVMByID` + `vm.Ping` + config GET. `Config` is a typed struct (`SCSI map[int]Drive`, `Net map[int]Net`, ...) instead of `*proxmox.VirtualMachineConfig` — `MergeSCSIs()`-style string scanning is replaced by iterating `cfg.SCSI`. |
| `cl.GetNodesForStorage(ctx, storage)` | `c.Cluster().Resources().List(ctx, ListFilter{Type: ResourceTypeStorage, Match: storage==id && status==available})` → map `.Node` | |
| `cl.GetClusterStorage(ctx, storage)` | same resources call, first match | |
| `cl.Client.ClusterStorage(ctx, storage)` (raw passthrough, different endpoint) | `c.Storage().Get(ctx, storageID)` | Root `/storage` config endpoint — genuinely different from the resources-based lookup above; keep them distinct in the new interface too. |
| `cl.GetStorageStatus(ctx, node, storage)` | `c.Nodes(node).Storage().Status(ctx, storageID)` | Error text match (`"No such storage"`) replaced by `proxmoxrest.IsNotFound(err)`. |
| `cl.GetStorageContent(ctx, node, storage)` | `c.Nodes(node).Storage().Content(storageID).List(ctx, nil)` | `Content()` now takes `storageID` at accessor-creation time (not per-call) — shipped shape differs slightly from §4.2's original sketch. |
| `cl.CreateVMDisk(ctx, vmid, node, storage, disk, sizeBytes)` | `c.Nodes(node).Storage().Content(storageID).Create(ctx, &storage.CreateVolumeOptions{...})` | |
| `cl.DeleteVMDisk(ctx, node, storage, disk)` + `task.WaitFor` | `c.Nodes(node).Storage().Content(storageID).Delete(ctx, volume, delay)` + `c.Nodes(node).Tasks().Wait(ctx, upid, nil)` (see §4.3) | |
| `cl.Client.Post(ctx, "/nodes/{n}/storage/{s}/content/{v}", params, &upid)` (copy/move volume) | `c.Nodes(node).Storage().Content(storageID).Copy(ctx, volume, &storage.CopyOptions{Target: ..., TargetNode: ...})` — **shipped, §4.2** | Used by `copyVolume` (`pkg/csi/utils.go`) and `MoveQemuDisk` (`pkg/tools/proxmox/volume.go`). |
| `cl.GetNextID(ctx, vmid)` | `c.Cluster().NextID(ctx, vmid)` | Wrapper's own retry-on-conflict loop (`lastVMID` cache) can move into the adapter or be dropped in favor of `go-proxmox-rest` retrying `SlowDown` responses natively. |
| `cl.CreateVM(ctx, node, options)` | `c.Nodes(node).Qemu().Create(ctx, &qemu.CreateOptions{VMID: ..., Config: qemu.Config{...}})` — **shipped, §4.1** | Only call site: `prepareReplication` (`pkg/csi/utils.go`), creates the placeholder VM used as the replication anchor. |
| `cl.DeleteVMByID(ctx, node, vmID)` | `c.Nodes(node).Qemu().Status(ctx, vmid)` (check running) → `Stop` if needed → `c.Nodes(node).Qemu().Delete(ctx, vmid, nil)`, each polled to completion | |
| `cl.Node(ctx, name)` + `node.VirtualMachine(ctx, vmid)` + `vm.Migrate(...)` | `c.Nodes(node).Qemu().Migrate(ctx, vmid, &qemu.MigrateOptions{Target: ..., Online: ...})` | `Online` is a plain `bool` now, no `proxmox.IntOrBool`. |
| `vm.Config(ctx, proxmox.VirtualMachineOption{...})` (attach/update/detach disk options) | `c.Nodes(node).Qemu().UpdateConfig(ctx, vmid, &qemu.Config{SCSI: map[int]qemu.Drive{lun: {...}}})` or `UpdateConfigAsync` for the hotplug/storage-allocating path (Proxmox's own recommendation, matches today's `vm.Config` + `task.WaitFor`) | The `wwn=`/comma-joined option string building in `attachVolume`/`updateVolume` is replaced by populating `qemu.Drive` struct fields directly. |
| `vm.UnlinkDisk(ctx, device, force)` | `c.Nodes(node).Qemu().Unlink(ctx, vmid, &qemu.UnlinkOptions{IDList: []string{device}, Force: force})` | |
| `cl.ResizeVMDisk(ctx, vmID, node, disk, size)` | `c.Nodes(node).Qemu().Resize(ctx, vmid, &qemu.ResizeOptions{Disk: disk, Size: size})` | |
| `px.GetHAGroupList(ctx)` | `c.Cluster().HA().Groups().List(ctx)` | Type is `ha.Group`, not the wrapper's own `HAGroup`. |
| `cl.Get(ctx, "/nodes/{n}/replication?guest={id}", &jobs)` (raw) | `c.Nodes(node).Replication().List(ctx, guest)` | |
| `cl.Client.Post(ctx, "/cluster/replication", params, nil)` (raw, create job) | `c.Cluster().Replication().Create(ctx, &replication.JobOptions{...})` | |
| `cl.Client.Delete(ctx, "/cluster/replication/{id}", nil)` (raw) | `c.Cluster().Replication().Delete(ctx, id, false, false)` | |
| `proxmox.NewTask(upid, cl.Client)` + `task.WaitFor(ctx, seconds)` / `WaitForCompleteStatus` | `c.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: ...})` — **shipped, §4.3** | Returns a `*tasks.FailedError` (not a bool `task.IsFailed`) on a non-`"OK"` exit status. |
| `goproxmox.GetVMUUID(vm)` / `GetVMSKU(vm)` (SMBIOS1 decode) | `qemu.SMBios1` already exists as a typed struct on `qemu.Config.SMBios1` (via `property.Unmarshal`) | Re-implement the two helpers as small functions over the typed field instead of `VMSMBIOS.UnmarshalString`. |
| `goproxmox.ErrVirtualMachineNotFound` / `ErrNodeNotFound` / `ErrNotFound` (sentinel errors checked with `errors.Is`) | none built in — `go-proxmox-rest` exposes `IsNotFound(err) bool` instead | The new adapter must keep returning the plugin's own sentinel errors (or a shared set) so every existing `errors.Is(err, goproxmox.ErrXxx)` call site keeps compiling and behaving the same. |

`cmd/pvecsictl/clean.go` and `pkg/csi/controller.go` never import `luthermonson/go-proxmox`
directly — they only touch the wrapper's public methods, all covered by the table above.

## 4. Gaps in `go-proxmox-rest` — closed

All three gaps identified below have shipped in `sergelogvinov/go-proxmox-rest` (VM creation and
volume copy/move are committed on `origin/main`; the task-wait helper exists in the local working
tree pending commit). Everything else in the §3 table already existed. Nothing here blocks
starting §5/§6 anymore.

### 4.1 `nodes/qemu`: create a new guest — done

`POST /nodes/{node}/qemu` had no typed client method — every other guest lifecycle operation
(clone, delete, migrate, resize, template, ...) did. Only call site in the plugin:
`prepareReplication` (`pkg/csi/utils.go`), which creates a minimal placeholder VM
(`defaultVMConfig()`: `boot`, `agent`, `machine`, `cores`, `memory`, `scsihw`) purely to act as
the anchor for a cluster replication job.

Shipped in `nodes/qemu/create.go`:

```go
// CreateOptions holds the parameters for Client.Create (POST
// /nodes/{node}/qemu). Config embeds the same fields Config/UpdateConfig
// already use, so a new guest's hardware is described with no second
// schema to learn.
type CreateOptions struct {
    Config           // embedded first (embeddedstructfieldcheck lint)
    VMID int          // required
    Pool string
    Start bool
}

func (c *Client) Create(ctx context.Context, opts *CreateOptions) (string, error)
```

Differs from the original sketch in one way: no `Template` field — `Create` stays a single HTTP
call (consistent with the rest of the package); mark a guest as a template via `Config.Template`
directly, or call `Client.Template` after the creation task completes. Reuses the package's own
unexported `encodeConfig` so it shares the exact indexed-field (`scsiN`, `netN`, ...) encoding
`UpdateConfig` uses. `fakeapi` support (`handleQemuCreate`, wired at `POST /nodes/{node}/qemu`)
and a round-trip test (`TestQemuCreate`) shipped alongside it.

### 4.2 `nodes/storage/content`: copy/move an existing volume — done

`POST /nodes/{node}/storage/{storage}/content/{volume}` — Proxmox's own docs mark this
"experimental" — copies/moves an existing volume to a new volume id and/or a different node.
Distinct from `Content(...).Create` (`POST .../content`, allocates a brand-new empty disk). Two
call sites in the plugin still hit it with a raw `cl.Client.Post`:

- `copyVolume` (`pkg/csi/utils.go`) — same-node or cross-node volume clone (CSI `CreateVolume`
  from a snapshot/source volume).
- `MoveQemuDisk` (`pkg/tools/proxmox/volume.go`) — cross-node volume move.

Shipped in `nodes/storage/copy.go`:

```go
// CopyOptions holds the parameters for Client.Content(storageID).Copy
// (POST /nodes/{node}/storage/{storage}/content/{volume}).
type CopyOptions struct {
    Target     string `url:"target"`               // destination volume name; required
    TargetNode string `url:"target_node,omitempty"` // empty keeps the source node
}

func (r *contentResource) Copy(ctx context.Context, volume string, opts *CopyOptions) (string, error)
```

Note the storage package's `Content()` accessor changed shape while this landed: it now takes
`storageID` at accessor-creation time (`Content(storageID).Copy(ctx, volume, opts)`, not
`Content().Copy(ctx, storageID, volume, opts)` as originally sketched) — matches §3's row for it.
The package doc comment in `nodes/storage/storage.go`, which previously listed `content`'s copy
endpoint among deliberately-excluded surface ("Proxmox's own source marks it experimental - do
not use"), was updated to carve out this one exception with the caveat preserved on `Copy`'s own
doc comment. `fakeapi` support (`handleStorageContentCopy`, dispatched from the existing
`POST /nodes/{node}/storage/{storage}/content/{volume}` route) and tests
(`TestStorageContentCopy`: same-node copy, cross-node move, absent-source error) shipped
alongside it.

### 4.3 a blocking task-wait helper — done

Was flagged optional (the plugin can poll `c.Nodes(node).Tasks().Status(ctx, upid)` itself,
matching the retry pattern already used in `waitAttachVolume`/`waitDetachVolume`
(`github.com/siderolabs/go-retry/retry`)), but every mutating call in today's code
(`task.WaitFor(ctx, seconds)`) leans on `luthermonson`'s built-in blocking waiter, so it was worth
landing upstream once rather than duplicating a poll loop across ~15 call sites in step 5.

Shipped in `nodes/tasks/wait.go`:

```go
// WaitOptions configures Client.Wait.
type WaitOptions struct {
    PollInterval time.Duration // default 5s, matching TaskStatusCheckInterval today
    Timeout      time.Duration // default 30s; callers doing long migrations pass 5*time.Minute etc.
}

// FailedError is Wait's error for a task that finished with an
// ExitStatus other than "OK". Use errors.As to recover UPID/ExitStatus.
type FailedError struct {
    UPID       string
    ExitStatus string
}

// Wait polls Status at PollInterval until the task stops running or
// ctx/Timeout is exceeded (errors.Is-detectable as context.DeadlineExceeded
// either way). Returns *FailedError for a non-"OK" exit status.
func (c *Client) Wait(ctx context.Context, upid string, opts *WaitOptions) error
```

Matches the original sketch closely; the one addition is the typed `*FailedError` (instead of a
bare `error`) so a caller can distinguish "task ran and failed" from "timed out"/"transport error"
via `errors.As`. Tests: `TestTasksWait` (instant-mode happy path), `TestTasksWaitFailed`
(concurrent `TaskController.Fail`), `TestTasksWaitTimeout` — all pass under `-race`.

## 5. Design decision: how "both modules at once" works — done

Rejected the originally-sketched shared-interface-plus-backend-flag design (a `ProxmoxClient`
interface both clients satisfy, selected per cluster via a `backend:`/`--proxmox-client` flag) in
favor of something more direct: `ProxmoxPool` simply builds and holds **both** concrete clients
for every configured cluster, side by side, and each call site picks the one it needs by which
client it was migrated to talk to. No interface, no runtime flag, no translating adapter package
— `pkg/proxmoxrest` from the original sketch doesn't get built at all.

Implemented in `pkg/proxmoxpool/pool.go`:

```go
type ProxmoxPool struct {
    // clients are the luthermonson/go-proxmox-backed clients. Not-yet-
    // migrated call sites use these through GetProxmoxCluster.
    clients map[string]*goproxmox.APIClient

    // clientsRest are the same clusters' clients on go-proxmox-rest.
    // Call sites switch from GetProxmoxCluster to GetProxmoxClusterRest
    // as they are migrated; clients/GetProxmoxCluster are removed once
    // none remain.
    clientsRest map[string]*proxmoxrest.Client
}

func (c *ProxmoxPool) GetProxmoxCluster(region string) (*goproxmox.APIClient, error)
func (c *ProxmoxPool) GetProxmoxClusterRest(region string) (*proxmoxrest.Client, error)
```

`NewProxmoxPool` builds one of each per configured cluster from the same resolved
`ProxmoxCluster` config (URL, insecure, token/password, including the token-from-file
resolution) — translating each auth/TLS option to its `go-proxmox-rest` equivalent
(`WithCredentials`→`WithPasswordAuth`, `WithAPIToken`→`WithTokenAuth`,
`WithHTTPClient(insecureTransport)`→`WithInsecure(true)`). Neither client makes a network call at
construction time (both are lazy — first request triggers auth), so this is safe to do
unconditionally without slowing startup or requiring a reachable cluster, and existing tests
(`TestNewClient`, `TestCheckClusters`) pass unchanged.

A migrated function is rewritten in place against `go-proxmox-rest`'s own typed API and its own
`proxmoxrest.IsNotFound`/`IsRateLimited`/`IsUnexpected` error helpers — not mapped back onto the
plugin's `goproxmox.ErrXxx` sentinels, since those are specific to the legacy client's semantics.
Callers further up the stack that currently do `errors.Is(err, goproxmox.ErrVirtualMachineNotFound)`
etc. need their checks updated as part of migrating whichever function they wrap, not left as a
translation shim.

This makes "support both modules at the same time" concrete at the level of an individual
function rather than a whole deployment: every call site in §3's table keeps working exactly as
before until its own commit switches it from `pool.GetProxmoxCluster(region)` to
`pool.GetProxmoxClusterRest(region)` and rewrites its body — so each migration step (§6 step 3) is
independently small, reviewable, and revertable.

## 6. Step-by-step plan

1. ~~Land the `go-proxmox-rest` gaps (§4.1, §4.2, §4.3) in that repo, with tests.~~ **Done** — see
   §4. Remaining before this step is fully closed out: commit/push §4.3 (still local-only), and
   tag a release (or agree on a pseudo-version) so `go.mod` here can pin something concrete
   instead of a bare `replace ... => ../go-proxmox-rest`.
2. ~~Vendor the dependency and wire up both clients.~~ **Done** — see §5:
   `github.com/sergelogvinov/go-proxmox-rest` is in `go.mod` (local `replace` for now, per step
   1's open item), and `pkg/proxmoxpool/pool.go`'s `NewProxmoxPool` builds a `*proxmoxrest.Client`
   alongside every legacy `*goproxmox.APIClient`, exposed via the new `GetProxmoxClusterRest`.
   No call sites changed yet — existing tests (`TestNewClient`, `TestCheckClusters`,
   `pkg/csi`, ...) pass unchanged.
3. **Migrate call sites function-by-function**, smallest blast radius first, each as its own
   commit/PR so `make conformance && make lint && make unit` gates every step. For each function:
   switch its `*goproxmox.APIClient` parameter (or the `pool.GetProxmoxCluster(region)` call
   feeding it) to `*proxmoxrest.Client` / `pool.GetProxmoxClusterRest(region)`, and rewrite the
   body against §3's mapping — including its own error handling (`proxmoxrest.IsNotFound(err)`
   etc.), not a translation back to the legacy sentinel errors.
   1. ~~`pkg/tools/proxmox/volume.go` (2 functions, self-contained) — uses §4.2's `Copy`.~~ **Done**
      — `WaitForVolumeDetach` now resolves the guest's node via `Cluster().Resources().List`
      then reads `qemu.Config.SCSI` directly (no more `MergeSCSIs()` string scanning);
      `MoveQemuDisk` calls the new `Content(storageID).Copy` and waits on the task via
      `Tasks().Wait`. Added `DeleteStorageVolume` alongside them for §6 step 3.2's use.
   2. ~~`cmd/pvecsictl/clean.go` (one command, easy to smoke-test manually).~~ **Done** — node/storage
      lookups moved to `Cluster().Resources().List` with a client-side `Match` filter (the
      `errors.Is(err, goproxmox.ErrNodeNotFound)` check is gone: an empty result replaces it),
      content listing/delete moved to `Nodes(node).Storage().Content(storageID)`. `cmd/pvecsictl/
      migrate.go` was migrated alongside it (same package) — just its `GetProxmoxCluster` call
      site, since its actual Proxmox logic lives entirely in the now-migrated
      `pkg/tools/proxmox/volume.go`.
   3. `pkg/proxmoxpool/pool.go` (`FindVMByNode`/`FindVMByUUID`/`GetNodeGroup`/`CheckClusters`).
   4. `pkg/csi/utils.go` (the bulk — volume attach/detach/create/copy/resize, replication
      helpers) — uses §4.1's `Create` for `prepareReplication`.
   5. `pkg/csi/controller.go` (the two direct `cl.Client.ClusterStorage` calls — become an
      ordinary `c.Storage().Get(ctx, storageID)` call once controller.go itself is migrated).

   Callers further up the stack that currently do
   `errors.Is(err, goproxmox.ErrVirtualMachineNotFound)` (etc.) against a migrated function's
   error need their checks updated in the same commit — see §5's note on this.
4. **Migrate the test fixtures.** Replace `test/cluster/cluster.go` (httpmock + raw
   `luthermonson` types) with `go-proxmox-rest`'s `fakeapi.NewCluster(...)` builder, updating
   `pkg/csi/utils_test.go` accordingly. This can happen incrementally alongside step 3 (both
   fixture styles coexist in the test suite until every call site is migrated) rather than as one
   big-bang swap.
5. **Burn-in.** Once a meaningful slice of step 3 has landed, run it in a dev/staging cluster for
   at least one full reconcile cycle covering create/attach/detach/resize/snapshot/replicate/
   delete — exercising both clients concurrently, since some functions will be on `go-proxmox-rest`
   and others still on `luthermonson` at any point in the migration.
6. **Remove the legacy path** once every call site listed in §3 has moved off `GetProxmoxCluster`:
   delete `clients`/`GetProxmoxCluster`/the legacy client construction from `pool.go`, drop
   `github.com/luthermonson/go-proxmox` and `github.com/sergelogvinov/go-proxmox` from `go.mod`,
   delete the commented `replace` lines for them, delete this document's §3/§4 (historical) or
   move it to `docs/deploy/` as an archived note per repo convention — decide at the time.

## 7. Risks / rollback

- There's no runtime backend flag in this design (§5) — rollback means reverting the specific
  commit that migrated a given function, not a config change. Keep step 3's commits small and
  independently revertable for exactly this reason.
- Field-name/type differences called out in §3 (`VMID int` vs `uint64`, `Online bool` vs
  `proxmox.IntOrBool`, typed `qemu.Drive`/`qemu.Net` vs raw property strings) are exactly the
  kind of thing that compiles fine but changes behavior at the margins (e.g. zero-value
  handling) — each function migrated in step 3 needs a manual review pass on top of tests, not
  just "tests pass."
- `go-proxmox-rest`'s retry policy (`retryCondition`: 5xx + `SlowDown` + connectivity errors,
  exponential backoff) differs from `luthermonson`'s; watch task-creation-under-load behavior
  (`GetNextID`'s conflict retry today, `cluster.NextID` tomorrow) during burn-in.
