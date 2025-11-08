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

package csi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	proxmox "github.com/luthermonson/go-proxmox"

	goproxmox "github.com/sergelogvinov/go-proxmox"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/metrics"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"
)

const (
	// TaskStatusCheckInterval is the interval in seconds to check the status of a task
	TaskStatusCheckInterval = 5
	// TaskTimeout is the timeout in seconds for all task
	TaskTimeout = 30

	// ErrorNotFound not found error message
	ErrorNotFound string = "not found"
)

// nolint:unused
func getNodeForVolume(ctx context.Context, cl *goproxmox.APIClient, vol *volume.Volume) (node string, err error) {
	node = vol.Node()
	if node == "" {
		node, err = cl.GetNodeForStorage(ctx, vol.Storage())
		if err != nil {
			return "", fmt.Errorf("failed to find best zone for storage %s: %v", vol.Storage(), err)
		}
	}

	return
}

func getVMByAttachedVolume(ctx context.Context, cl *goproxmox.APIClient, vol *volume.Volume) (int, int, error) {
	nodes := []string{}
	if vol.Node() != "" {
		nodes = append(nodes, vol.Node())
	}

	if len(nodes) == 0 {
		ns, err := cl.Client.Nodes(ctx)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to get node list: %v", err)
		}

		for _, n := range ns {
			nodes = append(nodes, n.Node)
		}
	}

	for _, n := range nodes {
		node, err := cl.Client.Node(ctx, n)
		if err != nil {
			return 0, 0, fmt.Errorf("unable to find node with name %s: %w", n, err)
		}

		vms, err := node.VirtualMachines(ctx)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to get vm list from node %s: %v", n, err)
		}

		for _, v := range vms {
			if vol.VMID() == fmt.Sprintf("%d", v.VMID) {
				continue
			}

			config, err := node.VirtualMachine(ctx, int(v.VMID))
			if err != nil {
				return 0, 0, fmt.Errorf("failed to get vm config: %v", err)
			}

			if lun, exist := isVolumeAttached(config.VirtualMachineConfig, vol.Disk()); exist {
				return int(v.VMID), lun, nil
			}
		}
	}

	return 0, 0, goproxmox.ErrNotFound
}

func getStorageContent(ctx context.Context, cl *goproxmox.APIClient, vol *volume.Volume) (*proxmox.StorageContent, error) {
	if vol.Node() == "" {
		return nil, errors.New("node is required")
	}

	n, err := cl.Client.Node(ctx, vol.Node())
	if err != nil {
		return nil, fmt.Errorf("unable to find node with name %s: %w", vol.Node(), err)
	}

	st, err := n.Storage(ctx, vol.Storage())
	if err != nil {
		if strings.Contains(err.Error(), "No such storage") {
			return nil, errors.New(ErrorNotFound)
		}

		return nil, err
	}

	contents, err := st.GetContent(ctx)
	if err != nil {
		return nil, err
	}

	for _, content := range contents {
		if content.Volid == vol.VolID() {
			return content, nil
		}
	}

	return nil, nil
}

func getVolumeSize(ctx context.Context, cl *goproxmox.APIClient, vol *volume.Volume) (int64, error) {
	st, err := getStorageContent(ctx, cl, vol)
	if err != nil {
		return 0, err
	}

	if st == nil {
		return 0, errors.New(ErrorNotFound)
	}

	return int64(st.Size), nil
}

func isVolumeAttached(vm *proxmox.VirtualMachineConfig, pvc string) (int, bool) {
	if pvc == "" {
		return 0, false
	}

	disks := vm.MergeSCSIs()
	for lun, disk := range disks {
		if strings.Contains(disk, pvc) {
			i, err := strconv.Atoi(strings.TrimPrefix(strings.Split(lun, ":")[0], deviceNamePrefix))
			if err != nil {
				return 0, false
			}

			return i, true
		}
	}

	return 0, false
}

func prepareReplication(ctx context.Context, cl *goproxmox.APIClient, node string, name string) (int, error) {
	id, err := cl.FindVMByName(ctx, name)
	if err != nil || id == 0 {
		id, err = cl.GetNextID(ctx, vmID+1)
		if err != nil {
			return 0, err
		}

		vm := defaultVMConfig()
		vm["name"] = name
		vm["vmid"] = id

		mc := metrics.NewMetricContext("createVm")
		if err = cl.CreateVM(ctx, node, vm); mc.ObserveRequest(err) != nil {
			return 0, err
		}
	}

	return id, nil
}

func createReplication(ctx context.Context, cl *goproxmox.APIClient, id int, vol *volume.Volume, params StorageParameters) error {
	cfg := map[string]string{
		"replicate": "1",
		"backup":    "1",
	}
	if _, err := attachVolume(ctx, cl, id, vol, cfg); err != nil {
		return err
	}

	schedule := "*/15"
	if params.ReplicateSchedule != "" {
		schedule = params.ReplicateSchedule
	}

	for i, z := range strings.Split(params.ReplicateZones, ",") {
		if z == vol.Node() {
			continue
		}

		repParams := map[string]interface{}{
			"id":       fmt.Sprintf("%d-%d", id, i),
			"type":     "local",
			"disable":  "0",
			"target":   z,
			"schedule": schedule,
			"comment":  "CSI Replication for Persistent Volume",
		}

		if err := cl.Client.Post(ctx, "/cluster/replication", repParams, nil); err != nil {
			return fmt.Errorf("failed to create replication: %v, repParams=%+v", err, repParams)
		}
	}

	return nil
}

func migrateReplication(ctx context.Context, cl *goproxmox.APIClient, target int, vol *volume.Volume) error {
	volid, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volumeID %s: %v", vol.VolumeID(), err)
	}

	if volid == vmID {
		return nil
	}

	sourceVM, err := cl.FindVMByID(ctx, uint64(volid))
	if err != nil {
		return fmt.Errorf("failed to find vm by id %d: %v", volid, err)
	}

	targetVM, err := cl.FindVMByID(ctx, uint64(target))
	if err != nil {
		return fmt.Errorf("failed to find vm by id %d: %v", target, err)
	}

	if sourceVM.Node == targetVM.Node {
		return nil
	}

	n, err := cl.Node(ctx, sourceVM.Node)
	if err != nil {
		return fmt.Errorf("unable to find node with name %s: %w", sourceVM.Node, err)
	}

	vm, err := n.VirtualMachine(ctx, volid)
	if err != nil {
		return fmt.Errorf("unable to find vm with id %d: %w", volid, err)
	}

	params := &proxmox.VirtualMachineMigrateOptions{
		Target: targetVM.Node,
		Online: false,
	}

	task, err := vm.Migrate(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to migrate vm config: %v", err)
	}

	if task != nil {
		if err = task.WaitFor(ctx, 5*60); err != nil {
			return fmt.Errorf("unable to migrate virtual machine: %w", err)
		}

		if task.IsFailed {
			return fmt.Errorf("unable to migrate virtual machine: %s", task.ExitStatus)
		}
	}

	return nil
}

func deleteReplication(ctx context.Context, cl *goproxmox.APIClient, vol *volume.Volume) error {
	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volumeID %s: %v", vol.VolumeID(), err)
	}

	if id != vmID {
		vm, err := cl.GetVMConfig(ctx, id)
		if err != nil {
			if strings.Contains(err.Error(), "machine not found") {
				return nil
			}

			return fmt.Errorf("failed to get vm config: %v", err)
		}

		if vm.Name != vol.PV() {
			return nil
		}

		if err := cl.Client.Delete(ctx, fmt.Sprintf("/cluster/replication/%d-%d", id, 0), nil); err != nil {
			if !strings.Contains(err.Error(), "no such job") {
				return fmt.Errorf("failed to delete replication schedule: %v", err)
			}
		}

		err = cl.DeleteVMByID(ctx, vm.Node, id)
		if err != nil {
			return fmt.Errorf("failed to delete replication vm: %v", err)
		}
	}

	return nil
}

func createVolume(ctx context.Context, cl *goproxmox.APIClient, vol *volume.Volume, sizeBytes int64) error {
	if vol.Node() == "" {
		return errors.New("node is required")
	}

	filename := strings.Split(vol.Disk(), "/")

	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volume vm id: %v", err)
	}

	err = cl.CreateVMDisk(ctx, id, vol.Node(), vol.Storage(), filename[len(filename)-1], sizeBytes)
	if err != nil {
		return fmt.Errorf("failed to create vm disk: %v", err)
	}

	return nil
}

func attachVolume(ctx context.Context, cl *goproxmox.APIClient, id int, vol *volume.Volume, options map[string]string) (map[string]string, error) {
	vm, err := cl.GetVMConfig(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get vm config: %v", err)
	}

	wwm := ""

	lun, exist := isVolumeAttached(vm.VirtualMachineConfig, vol.Disk())
	if exist {
		wwm = hex.EncodeToString([]byte(fmt.Sprintf("PVC-ID%02d", lun)))
	} else {
		disks := vm.VirtualMachineConfig.MergeSCSIs()

		for lun = 1; lun < 30; lun++ {
			device := deviceNamePrefix + strconv.Itoa(lun)

			if disks[device] == "" {
				wwm = hex.EncodeToString([]byte(fmt.Sprintf("PVC-ID%02d", lun)))

				options["wwn"] = "0x" + wwm

				opt := make([]string, 0, len(options))
				for k := range options {
					opt = append(opt, fmt.Sprintf("%s=%s", k, options[k]))
				}

				vmOptions := proxmox.VirtualMachineOption{
					Name:  device,
					Value: fmt.Sprintf("%s:%s,%s", vol.Storage(), vol.Disk(), strings.Join(opt, ",")),
				}

				task, err := vm.Config(ctx, vmOptions)
				if err != nil {
					return nil, fmt.Errorf("unable to attach disk: %v, options=%+v", err, vmOptions)
				}

				if err := task.WaitFor(ctx, 5*60); err != nil {
					return nil, fmt.Errorf("unable to attach virtual machine disk: %w", err)
				}

				break
			}
		}
	}

	if wwm != "" {
		return map[string]string{
			"DevicePath": "/dev/disk/by-id/wwn-0x" + wwm,
			"lun":        strconv.Itoa(lun),
		}, nil
	}

	return nil, fmt.Errorf("no free lun found")
}

func detachVolume(ctx context.Context, cl *goproxmox.APIClient, id int, vol *volume.Volume) error {
	vm, err := cl.GetVMConfig(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	if lun, ok := isVolumeAttached(vm.VirtualMachineConfig, vol.Disk()); ok {
		task, err := vm.UnlinkDisk(ctx, fmt.Sprintf("%s%d", deviceNamePrefix, lun), false)
		if err != nil {
			return fmt.Errorf("failed to unlink disk: %v", err)
		}

		if task != nil {
			if err := task.WaitFor(ctx, 5*60); err != nil {
				return fmt.Errorf("unable to detach virtual machine disk: %w", err)
			}
		}
	}

	return nil
}

func updateVolume(ctx context.Context, cl *goproxmox.APIClient, id int, vol *volume.Volume, options map[string]string) error {
	vm, err := cl.GetVMConfig(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	if lun, ok := isVolumeAttached(vm.VirtualMachineConfig, vol.Disk()); ok {
		disks := vm.VirtualMachineConfig.MergeSCSIs()
		if disk := disks[deviceNamePrefix+strconv.Itoa(lun)]; disk != "" {
			params := strings.Split(disk, ",")
			for _, param := range params {
				kv := strings.Split(param, "=")
				if len(kv) == 2 && options[kv[0]] == "" {
					options[kv[0]] = kv[1]
				}
			}
		}

		opt := make([]string, 0, len(options))
		for k := range options {
			opt = append(opt, fmt.Sprintf("%s=%s", k, options[k]))
		}

		vmOptions := proxmox.VirtualMachineOption{
			Name:  deviceNamePrefix + strconv.Itoa(lun),
			Value: fmt.Sprintf("%s:%s,%s", vol.Storage(), vol.Disk(), strings.Join(opt, ",")),
		}

		task, err := vm.Config(ctx, vmOptions)
		if err != nil {
			return fmt.Errorf("unable to update disk: %v, options=%+v", err, vmOptions)
		}

		if err := task.WaitFor(ctx, 5*60); err != nil {
			return fmt.Errorf("unable to update virtual machine disk: %w", err)
		}

		return nil
	}

	return fmt.Errorf("volume is not attached to VM %d", id)
}

func copyVolume(ctx context.Context, cl *goproxmox.APIClient, srcVol *volume.Volume, destVol *volume.Volume) error {
	if srcVol.Node() == "" {
		return errors.New("node is required")
	}

	if strings.Contains(destVol.Disk(), ".qcow2") {
		return errors.New("volume disk must not be qcow2 format")
	}

	params := map[string]interface{}{
		"target": destVol.Disk(),
	}

	if srcVol.Node() != destVol.Node() && destVol.Node() != "" {
		params["target_node"] = destVol.Node()
	}

	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/nodes/{node}/storage/{storage}/content/{volume}
	// Copy a volume. This is experimental code - do not use.
	var upid proxmox.UPID
	if err := cl.Client.Post(ctx, fmt.Sprintf("/nodes/%s/storage/%s/content/%s", srcVol.Node(), srcVol.Storage(), srcVol.Disk()), params, &upid); err != nil {
		return fmt.Errorf("failed to copy pvc: %v, params=%+v", err, params)
	}

	task := proxmox.NewTask(upid, cl.Client)
	if task != nil {
		_, completed, err := task.WaitForCompleteStatus(ctx, 4*60, 15)
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

// For shared storage, get all nodes that have access to the storage, to emulate real shared storage behavior.
// We need to find the node where the volume exists.
func getNodesForStorage(ctx context.Context, cl *goproxmox.APIClient, storage string) ([]string, error) {
	cluster, err := cl.Cluster(ctx)
	if err != nil {
		return nil, err
	}

	nodes := []string{}

	storageResources, err := cluster.Resources(ctx, "storage")
	if err != nil {
		return nil, err
	}

	for _, resource := range storageResources {
		if resource.Storage == storage && resource.Status == "available" {
			nodes = append(nodes, resource.Node)
		}
	}

	if len(nodes) == 0 {
		return nil, errors.New(ErrorNotFound)
	}

	return nodes, nil
}

func defaultVMConfig() map[string]interface{} {
	return map[string]interface{}{
		"boot":    "order=scsi0",
		"agent":   "0",
		"machine": "pc",
		"cores":   "1",
		"memory":  "512",
		"scsihw":  "virtio-scsi-single",
	}
}
