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

// Package proxmox implements tools to work with Proxmox VM.
package proxmox

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"
)

// WaitForVolumeDetach waits for the volume to be detached from the VM.
func WaitForVolumeDetach(client *pxapi.Client, vmName string, disk string) error {
	if vmName == "" {
		return nil
	}

	vmr, err := client.GetVmRefsByName(vmName)
	if err != nil || len(vmr) == 0 {
		return fmt.Errorf("failed to get vmID")
	}

	for {
		time.Sleep(5 * time.Second)

		vmConfig, err := client.GetVmConfig(vmr[0])
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		found := false

		for lun := 1; lun < 30; lun++ {
			device := fmt.Sprintf("scsi%d", lun)

			if vmConfig[device] != nil && strings.Contains(vmConfig[device].(string), disk) { //nolint:errcheck
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
func MoveQemuDisk(cluster *pxapi.Client, vol *volume.Volume, node string, taskTimeout int) error {
	vmParams := map[string]interface{}{
		"node":        vol.Node(),
		"target":      vol.Disk(),
		"target_node": node,
		"volume":      vol.Disk(),
	}

	oldTimeout := cluster.TaskTimeout
	cluster.TaskTimeout = taskTimeout

	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/nodes/{node}/storage/{storage}/content/{volume}
	// Copy a volume. This is experimental code - do not use.
	resp, err := cluster.CreateItemReturnStatus(vmParams, "/nodes/"+vol.Node()+"/storage/"+vol.Storage()+"/content/"+vol.Disk())
	if err != nil {
		return fmt.Errorf("failed to move pvc: %v, vmParams=%+v", err, vmParams)
	}

	var taskResponse map[string]interface{}

	if err = json.Unmarshal([]byte(resp), &taskResponse); err != nil {
		return fmt.Errorf("failed to parse response: %v", err)
	}

	for range 3 {
		if _, err = cluster.WaitForCompletion(taskResponse); err != nil {
			time.Sleep(2 * time.Second)

			continue
		}

		break
	}

	if err != nil {
		return fmt.Errorf("failed to wait for task completion: %v", err)
	}

	cluster.TaskTimeout = oldTimeout

	return nil
}
