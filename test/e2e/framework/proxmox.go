//go:build e2e

/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"context"
	"fmt"
	"strings"
	"time"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	proxmoxrest "github.com/sergelogvinov/go-proxmox-rest"
	"github.com/sergelogvinov/go-proxmox-rest/cluster"
	"github.com/sergelogvinov/go-proxmox-rest/cluster/replication"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/tasks"
	"github.com/sergelogvinov/go-proxmox-rest/task"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// NewProxmoxPool builds a ProxmoxPool from cfg.ProxmoxConfig (a
// cloud-config.yaml path, the same format the driver's own controller
// reads), for tests that want to verify state directly against the Proxmox
// API instead of only trusting what Kubernetes reports. Returns (nil, nil)
// when cfg.ProxmoxConfig is unset - callers should treat a nil pool as "skip
// this check", per docs/e2e.md's E2E_PROXMOX_CONFIG being optional.
func NewProxmoxPool(cfg Config) (*pxpool.ProxmoxPool, error) {
	if cfg.ProxmoxConfig == "" {
		return nil, nil //nolint:nilnil
	}

	cc, err := config.ReadCloudConfigFromFile(cfg.ProxmoxConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read proxmox cloud config %s: %w", cfg.ProxmoxConfig, err)
	}

	pool, err := pxpool.NewProxmoxPool(cc.Clusters)
	if err != nil {
		return nil, fmt.Errorf("failed to build proxmox pool from %s: %w", cfg.ProxmoxConfig, err)
	}

	return pool, nil
}

// VolumeDiskOptions finds the Proxmox VM backing the given Kubernetes node,
// locates the SCSI disk backing pv's volume handle among that VM's attached
// disks, and returns its Proxmox-side options (e.g. "iops_rd", "iops_wr",
// "mbps_rd", "mbps_wr", "backup", ...) parsed out of the disk's raw
// comma-separated config line - the ground truth for whatever a
// VolumeAttributesClass most recently applied, independent of whatever
// Kubernetes reports back.
func VolumeDiskOptions(ctx context.Context, pool *pxpool.ProxmoxPool, node *corev1.Node, pv *corev1.PersistentVolume) (map[string]string, error) {
	if pv.Spec.CSI == nil {
		return nil, fmt.Errorf("pv %s has no CSI volume source", pv.Name)
	}

	vol, err := volume.NewVolumeFromVolumeID(pv.Spec.CSI.VolumeHandle)
	if err != nil {
		return nil, fmt.Errorf("failed to parse volume handle %q of pv %s: %w", pv.Spec.CSI.VolumeHandle, pv.Name, err)
	}

	vmID, region, err := FindVMByNode(ctx, pool, node)
	if err != nil {
		return nil, fmt.Errorf("failed to find proxmox vm for node %s: %w", node.Name, err)
	}

	cl, err := pool.Get(region)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxmox cluster client for region %s: %w", region, err)
	}

	vmNode, err := findVMNode(ctx, cl, vmID)
	if err != nil {
		return nil, fmt.Errorf("failed to find proxmox node running vm %d (node %s): %w", vmID, node.Name, err)
	}

	vm, err := cl.Nodes(vmNode).Qemu().Config(ctx, vmID, &qemu.ConfigOptions{Current: true})
	if err != nil {
		return nil, fmt.Errorf("failed to get config of vm %d (node %s): %w", vmID, node.Name, err)
	}

	for _, drive := range vm.SCSI {
		if drive.File != vol.VolID() {
			continue
		}

		return parseDiskOptions(drive.String()), nil
	}

	return nil, fmt.Errorf("volume %s is not attached to vm %d (node %s)", vol.Disk(), vmID, node.Name)
}

// FindVMByNode searches every configured cluster for a VM whose name is
// prefixed by node.Name and whose SMBIOS UUID matches the node's reported
// SystemUUID, mirroring the unexported helper of the same name in
// pkg/csi/find.go.
func FindVMByNode(ctx context.Context, pool *pxpool.ProxmoxPool, node *corev1.Node) (vmID int, region string, err error) {
	for _, name := range pool.List() {
		vms, err := pool.Cluster(name).List(ctx, pxpool.ResourceKindVM,
			pxpool.WithMatch(func(rs *cluster.Resource) (bool, error) {
				return strings.HasPrefix(rs.Name, node.Name), nil
			}),
			pxpool.WithUUID(node.Status.NodeInfo.SystemUUID),
		)
		if err != nil {
			return 0, "", err
		}

		if len(vms) > 0 {
			return vms[0].VMID, name, nil
		}
	}

	return 0, "", pxpool.ErrInstanceNotFound
}

// findVMNode resolves the Proxmox node a guest currently runs on from its
// VMID, mirroring the unexported helper of the same name in
// pkg/csi/utils.go.
func findVMNode(ctx context.Context, cl *proxmoxrest.Client, vmid int) (string, error) {
	resources, err := cl.Cluster().Resources().List(ctx, cluster.ListFilter{
		Type:      cluster.ResourceTypeVM,
		GuestType: "qemu",
		VMID:      vmid,
	})
	if err != nil {
		return "", err
	}

	if len(resources) == 0 {
		return "", fmt.Errorf("virtual machine %d not found", vmid)
	}

	return resources[0].Node, nil
}

// parseDiskOptions parses a Proxmox disk config line, e.g.
// "local-lvm:vm-100-disk-0,backup=0,iops_rd=400,iops_wr=400,size=1G", into a
// key/value map.
func parseDiskOptions(raw string) map[string]string {
	opts := map[string]string{}

	for param := range strings.SplitSeq(raw, ",") {
		if kv := strings.SplitN(param, "=", 2); len(kv) == 2 {
			opts[kv[0]] = kv[1]
		}
	}

	return opts
}

// WaitForReplicationJob polls Proxmox's /cluster/replication until a
// replication job exists whose id has the "<vmID>-" prefix createReplication
// (pkg/csi/utils.go) itself posts when wiring up a replicated disk, and
// whose target is targetZone, then returns it - the real, Proxmox-side
// confirmation that zone replication (docs/options.md's replicate/
// replicateZones StorageClass parameters) was actually configured, since
// nothing about it is reflected on any Kubernetes object.
func WaitForReplicationJob(ctx context.Context, cl *proxmoxrest.Client, vmID int, targetZone string, timeout time.Duration) (*replication.Job, error) {
	var found *replication.Job

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		jobs, err := cl.Cluster().Replication().List(ctx)
		if err != nil {
			if isTransientError(err) {
				return false, nil
			}

			return false, err
		}

		for i := range jobs {
			if jobs[i].Guest == vmID && jobs[i].Target == targetZone {
				found = &jobs[i]

				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("no replication job for vm %d targeting zone %s appeared: %w", vmID, targetZone, err)
	}

	return found, nil
}

// WaitForReplicationJobGone polls Proxmox's /cluster/replication until no
// job with the given id remains - the real confirmation that
// DeleteVolume's deleteReplication (pkg/csi/utils.go) actually tore down
// the replication schedule, not just that the Kubernetes PV disappeared.
func WaitForReplicationJobGone(ctx context.Context, cl *proxmoxrest.Client, jobID string, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		jobs, err := cl.Cluster().Replication().List(ctx)
		if err != nil {
			if isTransientError(err) {
				return false, nil
			}

			return false, err
		}

		for i := range jobs {
			if jobs[i].ID == jobID {
				return false, nil
			}
		}

		return true, nil
	})
	if err != nil {
		return fmt.Errorf("replication job %s was not deleted: %w", jobID, err)
	}

	return nil
}

// WaitForShadowVMGone polls until the Proxmox VM with the given id no
// longer exists - the disk-owner "shadow" VM prepareReplication
// (pkg/csi/utils.go) creates to hold a replicated disk, torn down by
// deleteReplication alongside the replication job itself.
func WaitForShadowVMGone(ctx context.Context, cl *proxmoxrest.Client, vmID int, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		resources, err := cl.Cluster().Resources().List(ctx, cluster.ListFilter{
			Type:      cluster.ResourceTypeVM,
			GuestType: "qemu",
			VMID:      vmID,
		})

		switch {
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		case len(resources) == 0:
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("shadow vm %d was not deleted: %w", vmID, err)
	}

	return nil
}

// ControllerVMID returns the controller VM ID (features.controllerVmID) from
// cfg.ProxmoxConfig, falling back to config.DefaultControllerVMID the same
// way the driver does when it is unset. Shadow VMs for replicated volumes
// (pkg/csi/utils.go's prepareReplication) are allocated above this ID.
func ControllerVMID(cfg Config) (int, error) {
	cc, err := config.ReadCloudConfigFromFile(cfg.ProxmoxConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to read proxmox cloud config %s: %w", cfg.ProxmoxConfig, err)
	}

	return cc.Features.ControllerVMID, nil
}

// FindClustersByZone returns the client of every configured cluster that
// has a Proxmox node named zone, keyed by region. Node names are only
// unique within a cluster, so more than one region can match.
func FindClustersByZone(ctx context.Context, pool *pxpool.ProxmoxPool, zone string) (map[string]*proxmoxrest.Client, error) {
	clusters := map[string]*proxmoxrest.Client{}

	for _, region := range pool.List() {
		cl, err := pool.Get(region)
		if err != nil {
			return nil, err
		}

		nodes, err := cl.Cluster().Resources().List(ctx, cluster.ListFilter{
			Type: cluster.ResourceTypeNode,
			Match: func(rs *cluster.Resource) (bool, error) {
				return rs.Node == zone, nil
			},
		})
		if err != nil {
			return nil, err
		}

		if len(nodes) > 0 {
			clusters[region] = cl
		}
	}

	if len(clusters) == 0 {
		return nil, fmt.Errorf("zone %s not found in any configured proxmox cluster", zone)
	}

	return clusters, nil
}

// GuestExists reports whether any guest (VM or container) in the cluster
// holds the given VMID.
func GuestExists(ctx context.Context, cl *proxmoxrest.Client, vmID int) (bool, error) {
	resources, err := cl.Cluster().Resources().List(ctx, cluster.ListFilter{
		Type: cluster.ResourceTypeVM,
		VMID: vmID,
	})
	if err != nil {
		return false, err
	}

	return len(resources) > 0, nil
}

// CreateStoppedVM submits the creation of a minimal diskless VM with the
// given id and name on node, left stopped - just enough to occupy the VMID.
// It returns once Proxmox has accepted the request: from then on the id is
// the caller's to clean up, even if the returned task later fails. Wait for
// it with WaitForTask.
func CreateStoppedVM(ctx context.Context, cl *proxmoxrest.Client, node string, vmID int, name string) (*task.Task, error) {
	t, err := cl.Nodes(node).Qemu().Create(ctx, &qemu.CreateOptions{
		Name:   name,
		Cores:  new(1),
		Memory: &qemu.Memory{Current: new(128)},
		VMID:   vmID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create vm %d on node %s: %w", vmID, node, err)
	}

	return t, nil
}

// WaitForTask waits for a Proxmox task to finish successfully.
func WaitForTask(ctx context.Context, t *task.Task, timeout time.Duration) error {
	return t.Wait(ctx, &tasks.WaitOptions{Timeout: timeout})
}

// DeleteVM destroys the stopped VM with the given id on node.
func DeleteVM(ctx context.Context, cl *proxmoxrest.Client, node string, vmID int, timeout time.Duration) error {
	task, err := cl.Nodes(node).Qemu().Delete(ctx, vmID, &qemu.DeleteOptions{Purge: true})
	if err != nil {
		return fmt.Errorf("failed to delete vm %d on node %s: %w", vmID, node, err)
	}

	if err := task.Wait(ctx, &tasks.WaitOptions{Timeout: timeout}); err != nil {
		return fmt.Errorf("unable to delete vm %d on node %s: %w", vmID, node, err)
	}

	return nil
}
