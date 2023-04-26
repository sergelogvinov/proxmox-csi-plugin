// Package volume
package cloud

import (
	"fmt"
	"strings"
)

type volume struct {
	region  string
	zone    string
	storage string
	pvc     string
}

// NewVolume creates a new volume ID.
func NewVolume(region, zone, storage, pvc string) *volume {
	return &volume{
		region:  region,
		zone:    zone,
		storage: storage,
		pvc:     pvc,
	}
}

func NewVolumeFromVolumeID(volume string) (*volume, error) {
	return parseVolumeID(volume)
}

func parseVolumeID(vol string) (*volume, error) {
	parts := strings.Split(vol, "/")
	if len(parts) != 4 {
		return nil, fmt.Errorf("volumID must be in the format of region/zone/storageName/diskName")
	}

	return &volume{
		region:  parts[0],
		zone:    parts[1],
		storage: parts[2],
		pvc:     parts[3],
	}, nil
}

// VolumeID function returns the volume magic string.
func (v *volume) VolumeID() string {
	return v.region + "/" + v.zone + "/" + v.storage + "/" + v.pvc
}

// Region function returns the region in which the volume was created.
func (v *volume) Region() string {
	return v.region
}

// Zone function returns the zone in which the volume was created.
func (v *volume) Zone() string {
	return v.zone
}

// Storage function returns the Proxmox storage name.
func (v *volume) Storage() string {
	return v.storage
}

// PVC function returns the Proxmox PVC name.
func (v *volume) PVC() string {
	return v.pvc
}

// Cluster function returns the cluster name in which the volume was created.
func (v *volume) Cluster() string {
	return v.region
}

// Node function returns the node name in which the volume was created.
func (v *volume) Node() string {
	return v.zone
}
