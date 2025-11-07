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
	"fmt"
	"strings"
	"time"

	"github.com/luthermonson/go-proxmox"

	goproxmox "github.com/sergelogvinov/go-proxmox"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"
)

// WaitForVolumeDetach waits for the volume to be detached from the VM.
func WaitForVolumeDetach(ctx context.Context, client *goproxmox.APIClient, vmName string, pvc string) error {
	if vmName == "" {
		return nil
	}

	vmID, err := client.FindVMByName(ctx, vmName)
	if err != nil {
		return fmt.Errorf("failed to find vm by name %s: %v", vmName, err)
	}

	for {
		time.Sleep(5 * time.Second)

		vmConfig, err := client.GetVMConfig(ctx, vmID)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		found := false

		disks := vmConfig.VirtualMachineConfig.MergeSCSIs()
		for _, disk := range disks {
			if strings.Contains(disk, pvc) {
				found = true

				break
			}
		}

		if !found {
			return nil
		}
	}
}

// MoveQemuDisk moves the volume from one node to another.
func MoveQemuDisk(ctx context.Context, cluster *goproxmox.APIClient, vol *volume.Volume, node string, taskTimeout int) error {
	params := map[string]interface{}{
		"node":        vol.Node(),
		"target":      vol.Disk(),
		"target_node": node,
		"volume":      vol.Disk(),
	}

	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/nodes/{node}/storage/{storage}/content/{volume}
	// Copy a volume. This is experimental code - do not use.
	var upid proxmox.UPID
	if err := cluster.Client.Post(ctx, fmt.Sprintf("/nodes/%s/storage/%s/content/%s", vol.Node(), vol.Storage(), vol.Disk()), params, &upid); err != nil {
		return fmt.Errorf("failed to copy pvc: %v, params=%+v", err, params)
	}

	task := proxmox.NewTask(upid, cluster.Client)
	if task != nil {
		_, completed, err := task.WaitForCompleteStatus(ctx, taskTimeout/60, 60)
		if err != nil {
			return fmt.Errorf("unable to delete virtual machine disk: %w", err)
		}

		if completed {
			return nil
		}

		return fmt.Errorf("failed to copy disk, exit status: %s", task.ExitStatus)
	}

	return nil
}
