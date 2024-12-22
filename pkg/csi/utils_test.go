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
)

func TestIsVolumeAttached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg           string
		vmConfig      map[string]interface{}
		pvc           string
		expectedLun   int
		expectedExist bool
	}{
		{
			msg:           "Empty VM config",
			vmConfig:      map[string]interface{}{},
			pvc:           "",
			expectedLun:   0,
			expectedExist: false,
		},
		{
			msg: "Empty PVC",
			vmConfig: map[string]interface{}{
				"ide2":   "local:iso/ubuntu-20.04.1-live-server-amd64.iso,media=cdrom",
				"scsihw": "virtio-scsi-single",
				"scsi0":  "local-lvm:vm-100-disk-0,size=8G",
				"scsi5":  "local-lvm:vm-100-pvc-123,size=8G",
			},
			pvc:           "",
			expectedLun:   0,
			expectedExist: false,
		},
		{
			msg: "LUN 5",
			vmConfig: map[string]interface{}{
				"ide2":   "local:iso/ubuntu-20.04.1-live-server-amd64.iso,media=cdrom",
				"scsihw": "virtio-scsi-single",
				"scsi0":  "local-lvm:vm-100-disk-0,size=8G",
				"scsi5":  "local-lvm:vm-100-pvc-123,size=8G",
			},
			pvc:           "pvc-123",
			expectedLun:   5,
			expectedExist: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase

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

func TestRoundUpSizeBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg                 string
		volumeSize          int64
		allocationUnitBytes int64
		expected            int64
	}{
		{
			msg:                 "Zero size",
			volumeSize:          0,
			allocationUnitBytes: GiB,
			expected:            1024 * 1024 * 1024,
		},
		{
			msg:                 "KiB",
			volumeSize:          123,
			allocationUnitBytes: KiB,
			expected:            1024,
		},
		{
			msg:                 "MiB",
			volumeSize:          123,
			allocationUnitBytes: MiB,
			expected:            1024 * 1024,
		},
		{
			msg:                 "GiB",
			volumeSize:          123,
			allocationUnitBytes: GiB,
			expected:            1024 * 1024 * 1024,
		},
		{
			msg:                 "256MiB -> GiB",
			volumeSize:          256 * 1024 * 1024,
			allocationUnitBytes: GiB,
			expected:            1024 * 1024 * 1024,
		},
		{
			msg:                 "256MiB -> GiB/2",
			volumeSize:          256 * 1024 * 1024,
			allocationUnitBytes: 512 * MiB,
			expected:            512 * 1024 * 1024,
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			expected := RoundUpSizeBytes(testCase.volumeSize, testCase.allocationUnitBytes)
			assert.Equal(t, testCase.expected, expected)
		})
	}
}
