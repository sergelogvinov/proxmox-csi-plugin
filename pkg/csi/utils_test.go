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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/go-proxmox-rest/cluster"
	"github.com/sergelogvinov/go-proxmox-rest/fakeapi"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/lxc"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

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

func TestPrepareReplication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg        string
		vms        map[int]string
		containers []int
		failNextID bool
		expectedID int
		expectErr  bool
		expectNew  bool
	}{
		{
			msg:        "FirstShadowVM",
			vms:        map[int]string{9999: "controller"},
			expectedID: 10000,
			expectNew:  true,
		},
		{
			msg:        "NextIDTaken",
			vms:        map[int]string{9999: "controller", 10000: "pvc-other"},
			expectedID: 10001,
			expectNew:  true,
		},
		{
			msg:        "SkipsTakenIDsUpToGap",
			vms:        map[int]string{10000: "pvc-a", 10001: "pvc-b", 10003: "pvc-c"},
			expectedID: 10002,
			expectNew:  true,
		},
		{
			msg:        "SkipsContainers",
			vms:        map[int]string{10000: "pvc-a"},
			containers: []int{10001},
			expectedID: 10002,
			expectNew:  true,
		},
		{
			msg:        "ExistingShadowVM",
			vms:        map[int]string{10000: "pvc-other", 10005: "pvc-test"},
			failNextID: true,
			expectedID: 10005,
		},
		{
			msg:        "NextIDRejected",
			vms:        map[int]string{10000: "pvc-other"},
			failNextID: true,
			expectErr:  true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.msg, func(t *testing.T) {
			t.Parallel()

			fake := fakeapi.NewCluster(t, fakeapi.WithNodes("pve-1"))
			node := fake.Node("pve-1")

			for id, name := range testCase.vms {
				node.AddVM(id, &qemu.Config{Name: name})
			}

			for _, id := range testCase.containers {
				node.AddContainer(id, &lxc.Config{Hostname: "ct"})
			}

			if testCase.failNextID {
				fake.FailRequests("GET /cluster/nextid")
			}

			cl := fake.Client(t)

			id, err := prepareReplication(t.Context(), cl, "pve-1", "pvc-test", 9999)
			if testCase.expectErr {
				require.Error(t, err)

				resources, err := cl.Cluster().Resources().List(t.Context(), cluster.ListFilter{Type: cluster.ResourceTypeVM})
				require.NoError(t, err)
				assert.Len(t, resources, len(testCase.vms)+len(testCase.containers), "no vm should be created")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.expectedID, id)

			resources, err := cl.Cluster().Resources().List(t.Context(), cluster.ListFilter{Type: cluster.ResourceTypeVM, VMID: id})
			require.NoError(t, err)
			require.Len(t, resources, 1)
			assert.Equal(t, "pvc-test", resources[0].Name)

			if testCase.expectNew {
				assert.NotContains(t, testCase.vms, id)
			}
		})
	}
}
