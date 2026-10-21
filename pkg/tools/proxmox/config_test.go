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

package proxmox_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
	toolsproxmox "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/proxmox"
)

func TestVolumeLun(t *testing.T) {
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
		t.Run(testCase.msg, func(t *testing.T) {
			t.Parallel()

			lun, exist := toolsproxmox.VolumeLun(testCase.vmConfig, testCase.pvc)

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

func TestVMConfigs(t *testing.T) {
	t.Parallel()

	root := qemu.Drive{File: "local-lvm:vm-100-disk-0", Size: "8G"}
	pvc := qemu.Drive{File: "local-lvm:vm-9999-pvc-123", Size: "8G"}
	other := qemu.Drive{File: "local-lvm:vm-9999-pvc-456", Size: "8G"}

	tests := []struct {
		msg                 string
		current             map[int]qemu.Drive
		pending             map[int]qemu.Drive
		expectedDetached    bool
		expectedPendingOnly []int
		expectedFreeLun     []int
		expectedUsedLun     []int
	}{
		{
			msg:             "No pending changes, attached",
			current:         map[int]qemu.Drive{0: root, 1: pvc},
			pending:         map[int]qemu.Drive{0: root, 1: pvc},
			expectedFreeLun: []int{2},
			expectedUsedLun: []int{0, 1},
		},
		{
			msg:              "No pending changes, detached",
			current:          map[int]qemu.Drive{0: root},
			pending:          map[int]qemu.Drive{0: root},
			expectedDetached: true,
			expectedFreeLun:  []int{1},
			expectedUsedLun:  []int{0},
		},
		{
			msg:                 "Pending addition only",
			current:             map[int]qemu.Drive{0: root},
			pending:             map[int]qemu.Drive{0: root, 1: pvc},
			expectedPendingOnly: []int{1},
			expectedUsedLun:     []int{0, 1},
		},
		{
			msg:             "Current, pending deletion",
			current:         map[int]qemu.Drive{0: root, 1: pvc},
			pending:         map[int]qemu.Drive{0: root},
			expectedUsedLun: []int{0, 1},
		},
		{
			msg:                 "Other volume current, this volume pending at the same lun",
			current:             map[int]qemu.Drive{0: root, 1: other},
			pending:             map[int]qemu.Drive{0: root, 1: pvc},
			expectedPendingOnly: []int{1},
			expectedFreeLun:     []int{2},
			expectedUsedLun:     []int{0, 1},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.msg, func(t *testing.T) {
			t.Parallel()

			cfgs := &toolsproxmox.VMConfigs{
				Current: &qemu.Config{SCSI: testCase.current},
				Pending: &qemu.Config{SCSI: testCase.pending},
			}

			assert.Equal(t, testCase.expectedDetached, cfgs.Detached("pvc-123"), "detached")
			assert.Equal(t, testCase.expectedPendingOnly, cfgs.PendingOnly("pvc-123"), "pending only")

			for _, lun := range testCase.expectedFreeLun {
				assert.True(t, cfgs.LunFree(lun), "lun %d must be free", lun)
			}

			for _, lun := range testCase.expectedUsedLun {
				assert.False(t, cfgs.LunFree(lun), "lun %d must be in use", lun)
			}
		})
	}
}
