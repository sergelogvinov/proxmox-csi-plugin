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

package volume_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"
)

func TestNewVolume(t *testing.T) {
	v := volume.NewVolume("region", "zone", "storage", "disk")
	assert.NotNil(t, v)
	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "region", v.Region())
	assert.Equal(t, "zone", v.Zone())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "disk", v.Disk())
	assert.Equal(t, "region/zone/storage/disk", v.VolumeID())
}

func TestNewVolumeFormat(t *testing.T) {
	v := volume.NewVolume("region", "zone", "storage", "vm-100-disk-0", "raw")
	assert.NotNil(t, v)
	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "region", v.Region())
	assert.Equal(t, "zone", v.Zone())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "100/vm-100-disk-0.raw", v.Disk())
	assert.Equal(t, "region/zone/storage/100/vm-100-disk-0.raw", v.VolumeID())
}

func TestNewVolumeFromVolumeID(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region/zone/storage/vm-1000-disk")
	assert.Nil(t, err)
	assert.NotNil(t, v)
	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Zone())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "vm-1000-disk", v.Disk())
	assert.Equal(t, "1000", v.VMID())
	assert.Equal(t, "storage:vm-1000-disk", v.VolID())
}

func TestNewVolumeFromSharedVolumeID(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region//storage/vm-1000-disk")
	assert.Nil(t, err)
	assert.NotNil(t, v)

	v.SetNode("node")

	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "", v.Zone())
	assert.Equal(t, "node", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "vm-1000-disk", v.Disk())
	assert.Equal(t, "1000", v.VMID())
	assert.Equal(t, "storage:vm-1000-disk", v.VolID())
	assert.Equal(t, "region//storage/vm-1000-disk", v.VolumeID())
}

func TestNewVolumeFromVolumeIDWithFolder(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region/zone/storage/1000/folder/vm-1000-disk.raw")
	assert.Nil(t, err)
	assert.NotNil(t, v)
	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Zone())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "1000/folder/vm-1000-disk.raw", v.Disk())
	assert.Equal(t, "storage:1000/folder/vm-1000-disk.raw", v.VolID())
}

func TestNewVolumeFromSharedVolumeIDWithFolder(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region//storage/1000/folder/vm-1000-disk.raw")
	assert.Nil(t, err)
	assert.NotNil(t, v)

	v.SetNode("node")

	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "", v.Zone())
	assert.Equal(t, "node", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "1000/folder/vm-1000-disk.raw", v.Disk())
	assert.Equal(t, "storage:1000/folder/vm-1000-disk.raw", v.VolID())
	assert.Equal(t, "region//storage/1000/folder/vm-1000-disk.raw", v.VolumeID())
}

func TestNewVolumeFromVolumeIDError(t *testing.T) {
	_, err := volume.NewVolumeFromVolumeID("region/storage/disk")
	assert.NotNil(t, err)
	assert.Equal(t, "VolumeID must be in the format of region/zone/storageName/diskName", err.Error())
}

func TestCopyVolume(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region//storage/1000/folder.dev/vm-1000-disk.raw")
	assert.Nil(t, err)
	assert.NotNil(t, v)
	v.SetNode("node")

	copied := v.CopyVolume("vm-1000-disk-snap1")
	assert.NotNil(t, copied)
	assert.Equal(t, "region", copied.Cluster())
	assert.Equal(t, "", copied.Zone())
	assert.Equal(t, "node", copied.Node())
	assert.Equal(t, "storage", copied.Storage())
	assert.Equal(t, "1000/vm-1000-disk-snap1.raw", copied.Disk())
	assert.Equal(t, "storage:1000/vm-1000-disk-snap1.raw", copied.VolID())
	assert.Equal(t, "region//storage/1000/vm-1000-disk-snap1.raw", copied.VolumeID())
}
