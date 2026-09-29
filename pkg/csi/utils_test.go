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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

func TestIsVolumeAttached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg           string
		vmConfig      *qemu.Config
		pvc           string
		expectedLun   int
		expectedExist bool
	}{
		{
			msg:           "Empty VM config",
			vmConfig:      &qemu.Config{},
			pvc:           "",
			expectedLun:   0,
			expectedExist: false,
		},
		{
			msg: "Empty PVC",
			vmConfig: &qemu.Config{
				SCSI: map[int]qemu.Drive{
					0: {File: "local-lvm:vm-100-disk-0", Size: "8G"},
					5: {File: "local-lvm:vm-100-pvc-123", Size: "8G"},
				},
			},
			pvc:           "",
			expectedLun:   0,
			expectedExist: false,
		},
		{
			msg: "LUN 5",
			vmConfig: &qemu.Config{
				SCSI: map[int]qemu.Drive{
					0: {File: "local-lvm:vm-100-disk-0", Size: "8G"},
					5: {File: "local-lvm:vm-100-pvc-123", Size: "8G"},
				},
			},
			pvc:           "pvc-123",
			expectedLun:   5,
			expectedExist: true,
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			lun, exist := isVolumeAttached(testCase.vmConfig, testCase.pvc)

			if testCase.expectedExist {
				assert.True(t, exist)
				assert.Equal(t, testCase.expectedLun, lun)
			} else {
				assert.False(t, exist)
				assert.Equal(t, 0, lun)
			}
		})
	}
}

func TestGenerateWWN(t *testing.T) {
	t.Parallel()

	pv1 := "pvc-3f8e1c2a-1234-4a1b-9c3d-abcdefabcdef"
	pv2 := "pvc-9c3d1c2a-4321-4a1b-9c3d-fedcbafedcba"
	pv3 := "disk-1"

	wwn := generateWWN(pv1, 1)

	assert.Len(t, wwn, 16, "WWN must be a 16 hex character (64bit) string")
	assert.Equal(t, byte('5'), wwn[0], "WWN must start with the NAA-5 (IEEE Registered) nibble")

	assert.Equal(t, wwn, generateWWN(pv1, 1), "WWN must be deterministic for the same PV and lun")
	assert.NotEqual(t, wwn, generateWWN(pv1, 2), "WWN must differ across luns for the same PV")
	assert.NotEqual(t, wwn, generateWWN(pv2, 1), "WWN must differ across PVs for the same lun")

	// A PV name doesn't have to be a "pvc-<uuid>" string, e.g. for plain/shared disks.
	wwn3 := generateWWN(pv3, 1)

	assert.Len(t, wwn3, 16, "WWN must be a 16 hex character (64bit) string for a non-UUID PV name")
	assert.Equal(t, byte('5'), wwn3[0], "WWN must start with the NAA-5 (IEEE Registered) nibble")
	assert.Equal(t, wwn3, generateWWN(pv3, 1), "WWN must be deterministic for the same PV and lun")
	assert.NotEqual(t, wwn, wwn3, "WWN must differ across PVs for the same lun")
}

func TestDriveOptions(t *testing.T) {
	t.Parallel()

	drive := driveOptions(qemu.Drive{Size: "8G", File: "local-lvm:vm-100-disk-0"}, map[string]string{
		"backup":   "0",
		"iothread": "1",
		"iops_rd":  "100",
	})

	assert.Equal(t, "8G", drive.Size)
	assert.Equal(t, "local-lvm:vm-100-disk-0", drive.File)
	assert.Equal(t, new(false), drive.Backup)
	assert.Equal(t, new(true), drive.IOThread)
	assert.Equal(t, new(100), drive.IOPSRD)
}
