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

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"
)

func TestNewVolume(t *testing.T) {
	v := volume.NewVolume("region", "zone", "storage", "disk")
	assert.NotNil(t, v)

	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Node())

	assert.Equal(t, "region", v.Region())
	assert.Equal(t, "zone", v.Zone())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "disk", v.Disk())
	assert.Equal(t, "region/zone/storage/disk", v.VolumeID())
}

func TestNewVolumeFromVolumeID(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region/zone/storage/disk")
	assert.Nil(t, err)
	assert.NotNil(t, v)
	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "disk", v.Disk())
}

func TestNewVolumeFromVolumeIDWithFolder(t *testing.T) {
	v, err := volume.NewVolumeFromVolumeID("region/zone/storage/folder/disk")
	assert.Nil(t, err)
	assert.NotNil(t, v)
	assert.Equal(t, "region", v.Cluster())
	assert.Equal(t, "zone", v.Node())
	assert.Equal(t, "storage", v.Storage())
	assert.Equal(t, "folder/disk", v.Disk())
}

func TestNewVolumeFromVolumeIDError(t *testing.T) {
	_, err := volume.NewVolumeFromVolumeID("region/storage/disk")
	assert.NotNil(t, err)
	assert.Equal(t, "VolumeID must be in the format of region/zone/storageName/diskName", err.Error())
}
