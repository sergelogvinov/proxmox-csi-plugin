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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"
)

// GetNodeList retrieves the list of nodes in the Proxmox cluster.
func GetNodeList(client *pxapi.Client) ([]string, error) {
	data, err := client.GetNodeList()
	if err != nil {
		return nil, fmt.Errorf("failed to get node list: %v", err)
	}

	if data["data"] == nil {
		return nil, fmt.Errorf("failed to parse node list: %v", err)
	}

	nodes, ok := data["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to cast node list: %v", err)
	}

	nodeList := []string{}

	for _, item := range nodes { //nolint:errcheck
		node, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		if nodeName, ok := node["node"].(string); ok && nodeName != "" {
			nodeList = append(nodeList, nodeName)
		}
	}

	return nodeList, nil
}

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

// DeleteDisk delete the volume from all nodes.
func DeleteDisk(cluster *pxapi.Client, vol *volume.Volume) error {
	data, err := cluster.GetNodeList()
	if err != nil {
		return fmt.Errorf("failed to get node list: %v", err)
	}

	if data["data"] == nil {
		return fmt.Errorf("failed to parce node list: %v", err)
	}

	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volume vm id: %v", err)
	}

	for _, item := range data["data"].([]interface{}) { //nolint:errcheck
		node, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		vmr := pxapi.NewVmRef(id)
		vmr.SetNode(node["node"].(string)) //nolint:errcheck
		vmr.SetVmType("qemu")

		content, err := cluster.GetStorageContent(vmr, vol.Storage())
		if err != nil {
			return fmt.Errorf("failed to get storage content: %v", err)
		}

		images, ok := content["data"].([]interface{})
		if !ok {
			return fmt.Errorf("failed to cast images to map: %v", err)
		}

		volid := fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk())

		for i := range images {
			image, ok := images[i].(map[string]interface{})
			if !ok {
				return fmt.Errorf("failed to cast image to map: %v", err)
			}

			if image["volid"].(string) == volid && image["size"] != nil { //nolint:errcheck
				if _, err := cluster.DeleteVolume(vmr, vol.Storage(), vol.Disk()); err != nil {
					return fmt.Errorf("failed to delete volume: %s", vol.Disk())
				}
			}
		}
	}

	return nil
}

// MoveQemuDiskToStorage moves the volume from one storage to another.
func MoveQemuDiskToStorage(cluster *pxapi.Client, vol *volume.Volume, storage string, taskTimeout int) error {
	vmParams := map[string]interface{}{
		"node":    vol.Node(),
		"target":  fmt.Sprintf("%s-2", vol.Disk()),
		"volume":  vol.Disk(),
		"storage": storage,
	}

	oldTimeout := cluster.TaskTimeout
	cluster.TaskTimeout = taskTimeout

	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/nodes/{node}/qemu/{vmid}/move_disk
	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/nodes/{node}/storage/{storage}/content/{volume}
	// Move a volume. This is experimental code - do not use.
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

// CopyQemuDisk copies the volume from one volume to another with waiting for task completion.
func CopyQemuDisk(cluster *pxapi.Client, srcVol *volume.Volume, destVol *volume.Volume) error {
	taskResponse, err := CopyQemuDiskWithTask(cluster, srcVol, destVol)
	if err != nil {
		for {
			if !strings.Contains(err.Error(), "Wait timeout") {
				return err
			}

			if _, err = cluster.WaitForCompletion(taskResponse); err != nil {
				continue
			}

			break
		}
	}

	return nil
}

// CopyQemuDiskWithTask copies the volume from one volume to another and returns the task response.
func CopyQemuDiskWithTask(cluster *pxapi.Client, srcVol *volume.Volume, destVol *volume.Volume) (taskResponse map[string]interface{}, err error) {
	vmParams := map[string]interface{}{
		"node":   srcVol.Node(),
		"volume": srcVol.Disk(),
		"target": destVol.Disk(),
	}

	if srcVol.Node() != destVol.Node() {
		vmParams["target_node"] = destVol.Node()
	}

	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/nodes/{node}/storage/{storage}/content/{volume}
	// Copy a volume. This is experimental code - do not use.
	resp, err := cluster.CreateItemReturnStatus(vmParams, "/nodes/"+srcVol.Node()+"/storage/"+srcVol.Storage()+"/content/"+srcVol.Disk())
	if err != nil {
		return nil, fmt.Errorf("failed to copy pvc: %v, vmParams=%+v", err, vmParams)
	}

	if err = json.Unmarshal([]byte(resp), &taskResponse); err != nil {
		return taskResponse, fmt.Errorf("failed to parse response: %v", err)
	}

	if exitStatus, err := cluster.WaitForCompletion(taskResponse); err != nil {
		if exitStatus != "OK" {
			return nil, fmt.Errorf("failed to copy disk, exit status: %s", exitStatus)
		}

		return taskResponse, err
	}

	return nil, nil
}
