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
