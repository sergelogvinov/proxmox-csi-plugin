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

// Package volume implements the volume ID type and functions.
package volume

import (
	"fmt"
	"regexp"
	"strings"
)

var vmidre = regexp.MustCompile(`(^|-|/)vm-([1-9][0-9]{2,8})(-|$)`)

// Volume is the volume ID type.
type Volume struct {
	region  string
	zone    string
	node    string
	storage string
	disk    string
}

// NewVolume creates a new volume ID.
func NewVolume(region, zone, storage, disk string, format ...string) *Volume {
	if len(format) > 0 && format[0] != "" {
		parts := strings.SplitN(disk, "-", 3)
		if len(parts) == 3 {
			disk = fmt.Sprintf("%s/%s", parts[1], disk)
		}

		disk = fmt.Sprintf("%s.%s", disk, format[0])
	}

	return &Volume{
		region:  region,
		zone:    zone,
		node:    zone,
		storage: storage,
		disk:    disk,
	}
}

// NewVolumeFromVolumeID creates a new volume ID from a volume magic string.
func NewVolumeFromVolumeID(volume string) (*Volume, error) {
	return parseVolumeID(volume)
}

// CopyVolume creates a copy of the volume with a new disk name.
func (v *Volume) CopyVolume(volume string) *Volume {
	dotIndex := strings.LastIndex(v.disk, ".")
	if dotIndex != -1 {
		volume = fmt.Sprintf("%s/%s.%s", v.VMID(), volume, v.disk[dotIndex+1:])
	}

	return &Volume{
		region:  v.region,
		zone:    v.zone,
		node:    v.node,
		storage: v.storage,
		disk:    volume,
	}
}

func parseVolumeID(vol string) (*Volume, error) {
	parts := strings.SplitN(vol, "/", 4)
	if len(parts) != 4 {
		return nil, fmt.Errorf("VolumeID must be in the format of region/zone/storageName/diskName")
	}

	return &Volume{
		region:  parts[0],
		zone:    parts[1],
		node:    parts[1],
		storage: parts[2],
		disk:    parts[3],
	}, nil
}

// VolumeID function returns the volume magic string.
func (v *Volume) VolumeID() string {
	return v.region + "/" + v.zone + "/" + v.storage + "/" + v.disk
}

// VolumeSharedID function returns the shared volume magic string.
func (v *Volume) VolumeSharedID() string {
	return v.region + "//" + v.storage + "/" + v.disk
}

// Region function returns the region in which the volume was created.
func (v *Volume) Region() string {
	return v.region
}

// Zone function returns the zone in which the volume was created.
func (v *Volume) Zone() string {
	return v.zone
}

// Node function returns the node name in which the volume was created.
func (v *Volume) Node() string {
	return v.node
}

// Storage function returns the Proxmox storage name.
func (v *Volume) Storage() string {
	return v.storage
}

// Disk function returns the Proxmox disk name.
func (v *Volume) Disk() string {
	return v.disk
}

// Cluster function returns the cluster name in which the volume was created.
func (v *Volume) Cluster() string {
	return v.region
}

// VolID function returns the volume ID used in Proxmox.
func (v *Volume) VolID() string {
	return fmt.Sprintf("%s:%s", v.storage, v.disk)
}

// VMID function returns the vmID in which the volume was created.
func (v *Volume) VMID() string {
	matches := vmidre.FindStringSubmatch(v.disk)
	if matches == nil {
		return ""
	}

	return matches[2]
}

// PV function returns the kubernetes Persistent Volume (PV) name associated with the volume.
func (v *Volume) PV() string {
	parts := strings.SplitN(v.disk, "-", 3)
	if len(parts) != 3 {
		return ""
	}

	return strings.SplitN(parts[2], ".", 2)[0]
}

// SetZone sets the zone name for the volume.
func (v *Volume) SetZone(zone string) {
	if v.node == v.zone || v.node == "" {
		v.node = zone
	}

	v.zone = zone
}

// SetNode sets the node name for the volume.
func (v *Volume) SetNode(node string) {
	v.node = node
}

// SetStorage sets the storage name for the volume.
func (v *Volume) SetStorage(storage string) {
	v.storage = storage
}

// SetDisk sets the proxmox disk name.
func (v *Volume) SetDisk(disk string) {
	v.disk = disk
}
