// Package volume
package volume

import (
	"fmt"
	"strings"
)

type Volume struct {
	region  string
	zone    string
	storage string
	disk    string
}

// NewVolume creates a new volume ID.
func NewVolume(region, zone, storage, disk string) *Volume {
	return &Volume{
		region:  region,
		zone:    zone,
		storage: storage,
		disk:    disk,
	}
}

func NewVolumeFromVolumeID(volume string) (*Volume, error) {
	return parseVolumeID(volume)
}

func parseVolumeID(vol string) (*Volume, error) {
	parts := strings.Split(vol, "/")
	if len(parts) != 4 {
		return nil, fmt.Errorf("volumID must be in the format of region/zone/storageName/diskName")
	}

	return &Volume{
		region:  parts[0],
		zone:    parts[1],
		storage: parts[2],
		disk:    parts[3],
	}, nil
}

// VolumeID function returns the volume magic string.
func (v *Volume) VolumeID() string {
	return v.region + "/" + v.zone + "/" + v.storage + "/" + v.disk
}

// Region function returns the region in which the volume was created.
func (v *Volume) Region() string {
	return v.region
}

// Zone function returns the zone in which the volume was created.
func (v *Volume) Zone() string {
	return v.zone
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

// Node function returns the node name in which the volume was created.
func (v *Volume) Node() string {
	return v.zone
}
