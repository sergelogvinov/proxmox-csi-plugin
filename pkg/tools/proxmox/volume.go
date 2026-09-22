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

package proxmox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	proxmoxrest "github.com/sergelogvinov/go-proxmox-rest"
	"github.com/sergelogvinov/go-proxmox-rest/cluster"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/storage"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/tasks"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"
)

// WaitForVolumeDetach waits for the volume to be detached from the VM.
// vmID is the Proxmox VM ID of the Kubernetes node that was using the volume.
// If vmID is 0, the check is skipped (volume was not in use).
func WaitForVolumeDetach(ctx context.Context, client *proxmoxrest.Client, vmID int, pvc string) error {
	if vmID == 0 {
		return nil
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		resources, err := client.Cluster().Resources().List(ctx, cluster.ListFilter{
			Type:      cluster.ResourceTypeVM,
			GuestType: "qemu",
			VMID:      vmID,
		})
		if err != nil {
			return fmt.Errorf("failed to find vm %d: %v", vmID, err)
		}

		if len(resources) == 0 {
			return nil
		}

		cfg, err := client.Nodes(resources[0].Node).Qemu().Config(ctx, vmID, nil)
		if err != nil {
			return fmt.Errorf("failed to get vm config for VMID %d: %v", vmID, err)
		}

		found := false

		for _, disk := range cfg.SCSI {
			if strings.Contains(disk.File, pvc) {
				found = true

				break
			}
		}

		if !found {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// DeleteStorageVolume deletes a volume from a storage on a node, waiting
// for the deletion task to complete if Proxmox runs it as a background
// task instead of completing it synchronously.
func DeleteStorageVolume(ctx context.Context, client *proxmoxrest.Client, node, storageID, disk string) error {
	upid, err := client.Nodes(node).Storage().Content(storageID).Delete(ctx, disk, 0)
	if err != nil {
		return err
	}

	if upid == "" {
		return nil
	}

	if err := client.Nodes(node).Tasks().Wait(ctx, upid, nil); err != nil {
		if failed, ok := errors.AsType[*tasks.FailedError](err); ok {
			return fmt.Errorf("exit status: %s", failed.ExitStatus)
		}

		return err
	}

	return nil
}

// MoveQemuDisk moves the volume from one node to another.
func MoveQemuDisk(ctx context.Context, client *proxmoxrest.Client, vol *volume.Volume, node string, taskTimeout int) error {
	upid, err := client.Nodes(vol.Node()).Storage().Content(vol.Storage()).Copy(ctx, vol.Disk(), &storage.CopyOptions{
		Target:     vol.Disk(),
		TargetNode: node,
	})
	if err != nil {
		return fmt.Errorf("failed to copy pvc: %v", err)
	}

	if upid == "" {
		return nil
	}

	if err := client.Nodes(vol.Node()).Tasks().Wait(ctx, upid, &tasks.WaitOptions{
		PollInterval: 15 * time.Second,
		Timeout:      time.Duration(taskTimeout) * time.Second,
	}); err != nil {
		if failed, ok := errors.AsType[*tasks.FailedError](err); ok {
			return fmt.Errorf("failed to copy disk, exit status: %s", failed.ExitStatus)
		}

		return fmt.Errorf("unable to move virtual machine disk: %w", err)
	}

	return nil
}
