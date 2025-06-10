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
	"fmt"
	"sync"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
)

// VMLocks is a structure that protects to multiple VMs changes.
type VMLocks struct {
	mux   sync.Mutex
	locks map[string]*sync.Mutex
}

// NewVMLocks creates a new instance of VMLocks.
func NewVMLocks() *VMLocks {
	return &VMLocks{
		locks: make(map[string]*sync.Mutex),
	}
}

// Lock method locks a VM by its name.
func (v *VMLocks) Lock(name string) {
	v.mux.Lock()
	defer v.mux.Unlock()

	if _, exists := v.locks[name]; !exists {
		v.locks[name] = &sync.Mutex{}
	}

	v.locks[name].Lock()
}

// Unlock method unlocks a VM by its name.
func (v *VMLocks) Unlock(name string) {
	v.mux.Lock()
	defer v.mux.Unlock()

	if lock, exists := v.locks[name]; exists {
		lock.Unlock()
		delete(v.locks, name)
	}
}

// CreateQemuVM creates a new simple Qemu VM on the given node with the given name.
func CreateQemuVM(client *pxapi.Client, vmr *pxapi.VmRef, name string) error {
	vm := map[string]interface{}{}
	vm["vmid"] = vmr.VmId()
	vm["node"] = vmr.Node()
	vm["name"] = name
	vm["boot"] = "order=scsi0"
	vm["agent"] = "0"
	vm["machine"] = "pc"
	vm["cores"] = "1"
	vm["memory"] = "512"
	vm["scsihw"] = "virtio-scsi-single"

	_, err := client.CreateQemuVm(vmr.Node(), vm)
	if err != nil {
		return fmt.Errorf("failed to create vm: %v", err)
	}

	return nil
}

// DeleteQemuVM delete the Qemu VM on the given node with the given name.
func DeleteQemuVM(client *pxapi.Client, vmr *pxapi.VmRef) error {
	params := map[string]interface{}{}
	params["purge"] = "1"

	if _, err := client.DeleteVmParams(vmr, params); err != nil {
		return fmt.Errorf("failed to delete vm %d: %v", vmr.VmId(), err)
	}

	return nil
}

// SetQemuVMReplication sets the replication configuration for the given VM.
func SetQemuVMReplication(client *pxapi.Client, vmr *pxapi.VmRef, node string, schedule string) error {
	if schedule == "" {
		schedule = "*/15"
	}

	vmParams := map[string]interface{}{
		"id":       fmt.Sprintf("%d-0", vmr.VmId()),
		"type":     "local",
		"target":   node,
		"schedule": schedule,
		"comment":  "CSI Replication for PV",
	}

	// POST https://pve.proxmox.com/pve-docs/api-viewer/index.html#/cluster/replication
	_, err := client.CreateItemReturnStatus(vmParams, "/cluster/replication")
	if err != nil {
		return fmt.Errorf("failed to create replication: %v, vmParams=%+v", err, vmParams)
	}

	return nil
}
