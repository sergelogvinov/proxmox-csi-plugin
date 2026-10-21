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
	"strings"

	proxmoxrest "github.com/sergelogvinov/go-proxmox-rest"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

// VMConfigs holds both views of a guest's configuration. Proxmox records a
// disk hotplug or hot-unplug that failed as a pending change, so the two views
// disagree exactly when an attach or detach did not complete.
type VMConfigs struct {
	// Current is the configuration actually applied to the guest.
	Current *qemu.Config
	// Pending is Proxmox's default view: Current with the pending changes
	// applied, so disks pending addition appear and disks pending deletion
	// disappear.
	Pending *qemu.Config
}

// GetVMCurrentConfig returns the guest's current (applied) configuration,
// ignoring pending changes.
func GetVMCurrentConfig(ctx context.Context, client *proxmoxrest.Client, node string, vmID int) (*qemu.Config, error) {
	return client.Nodes(node).Qemu().Config(ctx, vmID, &qemu.ConfigOptions{Current: true})
}

// GetVMConfigs returns both the current and the pending view of the guest's configuration.
func GetVMConfigs(ctx context.Context, client *proxmoxrest.Client, node string, vmID int) (*VMConfigs, error) {
	current, err := GetVMCurrentConfig(ctx, client, node, vmID)
	if err != nil {
		return nil, err
	}

	// nil options on purpose: Proxmox's default view is the pending one.
	pending, err := client.Nodes(node).Qemu().Config(ctx, vmID, nil)
	if err != nil {
		return nil, err
	}

	return &VMConfigs{Current: current, Pending: pending}, nil
}

// VolumeLun reports whether a SCSI disk of cfg is the volume pvc, returning
// its lun.
func VolumeLun(cfg *qemu.Config, pvc string) (int, bool) {
	if pvc == "" {
		return 0, false
	}

	for lun, disk := range cfg.SCSI {
		if strings.Contains(disk.File, pvc) {
			return lun, true
		}
	}

	return 0, false
}

// Detached reports whether pvc is gone from the guest, which it is only if
// neither view has it: a disk still current but pending deletion is a
// hot-unplug that failed, and a disk pending addition is a hotplug that failed.
func (c *VMConfigs) Detached(pvc string) bool {
	if _, ok := VolumeLun(c.Current, pvc); ok {
		return false
	}

	_, ok := VolumeLun(c.Pending, pvc)

	return !ok
}

// PendingOnly returns the SCSI luns where only the pending view has pvc:
// hotplugs of pvc that failed.
func (c *VMConfigs) PendingOnly(pvc string) []int {
	if pvc == "" {
		return nil
	}

	var luns []int

	for lun, disk := range c.Pending.SCSI {
		if !strings.Contains(disk.File, pvc) {
			continue
		}

		if current, ok := c.Current.SCSI[lun]; ok && strings.Contains(current.File, pvc) {
			continue
		}

		luns = append(luns, lun)
	}

	return luns
}

// LunFree reports whether the SCSI slot is empty in both views.
func (c *VMConfigs) LunFree(lun int) bool {
	_, inCurrent := c.Current.SCSI[lun]
	_, inPending := c.Pending.SCSI[lun]

	return !inCurrent && !inPending
}
