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
	"errors"
	"fmt"
	"strings"
	"time"

	goproxmox "github.com/sergelogvinov/go-proxmox"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// scsiDeviceNamePrefix mirrors the unexported deviceNamePrefix in
// pkg/csi/controller.go: every volume this driver attaches lands on a SCSI
// bus slot, "scsiN".
const scsiDeviceNamePrefix = "scsi"

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

	vmID, region, err := pool.FindVMByNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("failed to find proxmox vm for node %s: %w", node.Name, err)
	}

	cl, err := pool.GetProxmoxCluster(region)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxmox cluster client for region %s: %w", region, err)
	}

	vm, err := cl.GetVMConfig(ctx, vmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get config of vm %d (node %s): %w", vmID, node.Name, err)
	}

	for slot, raw := range vm.VirtualMachineConfig.MergeSCSIs() {
		backingVolume, _, _ := strings.Cut(raw, ",")
		if !strings.HasPrefix(slot, scsiDeviceNamePrefix) || backingVolume != vol.VolID() {
			continue
		}

		return parseDiskOptions(raw), nil
	}

	return nil, fmt.Errorf("volume %s is not attached to vm %d (node %s)", vol.Disk(), vmID, node.Name)
}

// parseDiskOptions parses a Proxmox disk config line, e.g.
// "local-lvm:vm-100-disk-0,backup=0,iops_rd=400,iops_wr=400,size=1G", into a
// key/value map.
func parseDiskOptions(raw string) map[string]string {
	opts := map[string]string{}

	for _, param := range strings.Split(raw, ",") {
		if kv := strings.SplitN(param, "=", 2); len(kv) == 2 {
			opts[kv[0]] = kv[1]
		}
	}

	return opts
}

// ReplicationJob is the subset of a Proxmox cluster replication job config
// (GET /cluster/replication) this suite inspects.
type ReplicationJob struct {
	ID     string `json:"id"`
	Target string `json:"target"`
}

// WaitForReplicationJob polls Proxmox's /cluster/replication until a
// replication job exists whose id has the "<vmID>-" prefix createReplication
// (pkg/csi/utils.go) itself posts when wiring up a replicated disk, and
// whose target is targetZone, then returns it - the real, Proxmox-side
// confirmation that zone replication (docs/options.md's replicate/
// replicateZones StorageClass parameters) was actually configured, since
// nothing about it is reflected on any Kubernetes object.
func WaitForReplicationJob(ctx context.Context, cl *goproxmox.APIClient, vmID int, targetZone string, timeout time.Duration) (*ReplicationJob, error) {
	var found *ReplicationJob

	prefix := fmt.Sprintf("%d-", vmID)

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		var jobs []ReplicationJob

		if err := cl.Get(ctx, "/cluster/replication", &jobs); err != nil {
			if isTransientError(err) {
				return false, nil
			}

			return false, err
		}

		for i := range jobs {
			if strings.HasPrefix(jobs[i].ID, prefix) && jobs[i].Target == targetZone {
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
func WaitForReplicationJobGone(ctx context.Context, cl *goproxmox.APIClient, jobID string, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		var jobs []ReplicationJob

		if err := cl.Get(ctx, "/cluster/replication", &jobs); err != nil {
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
func WaitForShadowVMGone(ctx context.Context, cl *goproxmox.APIClient, vmID int, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := cl.GetVMByID(ctx, uint64(vmID)) //nolint:gosec

		switch {
		case errors.Is(err, goproxmox.ErrVirtualMachineNotFound):
			return true, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("shadow vm %d was not deleted: %w", vmID, err)
	}

	return nil
}
