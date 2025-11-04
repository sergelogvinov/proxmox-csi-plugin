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

package csi_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/helpers/ptr"
)

func Test_ExtractAndDefaultParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg     string
		params  map[string]string
		storage csi.StorageParameters
	}{
		{
			msg: "Empty params",
			params: map[string]string{
				csi.StorageIDKey: "local-lvm",
			},
			storage: csi.StorageParameters{
				StorageID: "local-lvm",
				Backup:    ptr.Ptr(false),
				IOThread:  true,
			},
		},
		{
			msg: "SSD disk",
			params: map[string]string{
				csi.StorageIDKey:    "local-lvm",
				csi.StorageSSDKey:   "true",
				csi.StorageCacheKey: "directsync",
			},
			storage: csi.StorageParameters{
				StorageID: "local-lvm",
				Cache:     "directsync",
				Backup:    ptr.Ptr(false),
				IOThread:  true,
				SSD:       ptr.Ptr(true),
				Discard:   "on",
			},
		},
		{
			msg: "disk limits",
			params: map[string]string{
				csi.StorageIDKey:       "local-lvm",
				csi.StorageSSDKey:      "true",
				csi.StorageDiskIOPSKey: "100",
			},
			storage: csi.StorageParameters{
				StorageID: "local-lvm",
				Backup:    ptr.Ptr(false),
				IOThread:  true,
				SSD:       ptr.Ptr(true),
				Discard:   "on",
				Iops:      ptr.Ptr(100),
				IopsRead:  ptr.Ptr(100),
				IopsWrite: ptr.Ptr(100),
			},
		},
		{
			msg: "ovverid disk backup",
			params: map[string]string{
				csi.StorageIDKey:       "local-lvm",
				csi.StorageSSDKey:      "true",
				csi.StorageDiskIOPSKey: "100",
				"backup":               "1",
			},
			storage: csi.StorageParameters{
				StorageID: "local-lvm",
				Backup:    ptr.Ptr(true),
				IOThread:  true,
				SSD:       ptr.Ptr(true),
				Discard:   "on",
				Iops:      ptr.Ptr(100),
				IopsRead:  ptr.Ptr(100),
				IopsWrite: ptr.Ptr(100),
			},
		},
		{
			msg: "replication disk",
			params: map[string]string{
				csi.StorageIDKey: "local-lvm",
				"backup":         "true",
				"replicate":      "true",
				"replicateZones": "zone1,zone2",
			},
			storage: csi.StorageParameters{
				StorageID:      "local-lvm",
				Backup:         ptr.Ptr(true),
				IOThread:       true,
				Replicate:      true,
				ReplicateZones: "zone1,zone2",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			storage, err := csi.ExtractAndDefaultParameters(testCase.params)

			assert.Nil(t, err)
			assert.Equal(t, testCase.storage, storage)
		})
	}
}

func Test_ToMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg     string
		storage csi.StorageParameters
		params  map[string]string
	}{
		{
			msg: "Empty params",
			storage: csi.StorageParameters{
				StorageID:     "local-lvm",
				StorageFormat: "raw",
			},
			params: map[string]string{
				"iothread":  "0",
				"replicate": "0",
			},
		},
		{
			msg: "Params with IOThread and limits",
			storage: csi.StorageParameters{
				Cache:     "directsync",
				IOThread:  true,
				IopsRead:  ptr.Ptr(100),
				IopsWrite: ptr.Ptr(100),
			},
			params: map[string]string{
				"cache":     "directsync",
				"iothread":  "1",
				"replicate": "0",
				"iops_rd":   "100",
				"iops_wr":   "100",
			},
		},
		{
			msg: "Params with replication",
			storage: csi.StorageParameters{
				Cache:             "directsync",
				IOThread:          true,
				Replicate:         true,
				ReplicateZones:    "zone1,zone2",
				ReplicateSchedule: "*/30",
			},
			params: map[string]string{
				"cache":     "directsync",
				"iothread":  "1",
				"replicate": "1",
			},
		},
		{
			msg: "resize parameters",
			storage: csi.StorageParameters{
				ResizeRequired:  ptr.Ptr(true),
				ResizeSizeBytes: 1024 * 1024 * 1024,
			},
			params: map[string]string{
				"iothread":        "0",
				"replicate":       "0",
				"resizeRequired":  "1",
				"resizeSizeBytes": "1073741824",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			storage := testCase.storage.ToMap()
			assert.Equal(t, testCase.params, storage)
		})
	}
}

func Test_ExtractModifyVolumeParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg     string
		params  map[string]string
		storage csi.ModifyVolumeParameters
	}{
		{
			msg:     "Empty params",
			params:  map[string]string{},
			storage: csi.ModifyVolumeParameters{},
		},
		{
			msg: "Backup volume",
			params: map[string]string{
				"backup": "true",
			},
			storage: csi.ModifyVolumeParameters{
				Backup: ptr.Ptr(true),
			},
		},
		{
			msg: "BW limits",
			params: map[string]string{
				"diskIOPS": "100",
				"diskMBps": "100",
			},
			storage: csi.ModifyVolumeParameters{
				Iops:           ptr.Ptr(100),
				IopsRead:       ptr.Ptr(100),
				IopsWrite:      ptr.Ptr(100),
				SpeedMbps:      ptr.Ptr(100),
				ReadSpeedMbps:  ptr.Ptr(100),
				WriteSpeedMbps: ptr.Ptr(100),
			},
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			storage, err := csi.ExtractModifyVolumeParameters(testCase.params)

			assert.Nil(t, err)
			assert.Equal(t, testCase.storage, storage)
		})
	}
}

func Test_MergeMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg      string
		storage  csi.ModifyVolumeParameters
		params   map[string]string
		expected map[string]string
	}{
		{
			msg:     "Empty modify params",
			storage: csi.ModifyVolumeParameters{},
			params: map[string]string{
				"storage": "lvm",
				"ssd":     "true",
			},
			expected: map[string]string{
				"storage": "lvm",
				"ssd":     "true",
			},
		},
		{
			msg: "Backup param",
			storage: csi.ModifyVolumeParameters{
				Backup: ptr.Ptr(true),
			},
			params: map[string]string{
				"storage":   "lvm",
				"ssd":       "true",
				"blockSize": "1024",
			},
			expected: map[string]string{
				"backup":    "1",
				"storage":   "lvm",
				"ssd":       "true",
				"blockSize": "1024",
			},
		},
		{
			msg:     "Resize param",
			storage: csi.ModifyVolumeParameters{},
			params: map[string]string{
				"storage":         "lvm",
				"resizeRequired":  "true",
				"resizeSizeBytes": "1024",
			},
			expected: map[string]string{
				"storage":         "lvm",
				"resizeRequired":  "true",
				"resizeSizeBytes": "1024",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			storage := testCase.storage.MergeMap(testCase.params)
			assert.Equal(t, testCase.expected, storage)
		})
	}
}
