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

package cluster

import (
	"testing"

	"github.com/sergelogvinov/go-proxmox-rest/fakeapi"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/storage"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
)

// SetupFakeCluster starts a fake Proxmox cluster and points region "cluster-1"'s
// REST client at it. Returns the fake cluster so a test can drive fault
// injection (e.g. Cluster.FailNode) directly. Region "cluster-2" (used only by
// the CapMoxProvider test config) is left pointed at its static, unreachable
// config URL: no test in this suite ever issues a request against it.
func SetupFakeCluster(t *testing.T, pool *pxpool.ProxmoxPool) *fakeapi.Cluster {
	t.Helper()

	cl := fakeapi.NewCluster(t, fakeapi.WithNodes("pve-1", "pve-2", "pve-3"))

	pve1, pve2 := cl.Node("pve-1"), cl.Node("pve-2")

	pve1.AddStorage("smb", "cifs", fakeapi.WithShared(),
		fakeapi.WithVolume(storage.Volume{VolID: "smb:9999/vm-9999-volume-smb.raw", Format: "raw", Size: 1 << 30, VMID: 9999}))

	for _, n := range []*fakeapi.Node{pve1, pve2} {
		n.AddStorage("rbd", "dir", fakeapi.WithShared(),
			fakeapi.WithVolume(storage.Volume{VolID: "rbd:9999/vm-9999-volume-rbd.raw", Format: "raw", Size: 1 << 30, VMID: 9999}))
		n.AddStorage("zfs", "zfspool",
			fakeapi.WithCapacity(100<<30, 50<<30, 50<<30))
	}

	pve1.AddStorage("local-lvm", "lvm",
		fakeapi.WithCapacity(100<<30, 50<<30, 50<<30),
		fakeapi.WithVolume(storage.Volume{VolID: "local-lvm:vm-9999-pvc-123", Format: "raw", Size: csi.MinChunkSizeBytes}),
		fakeapi.WithVolume(storage.Volume{VolID: "local-lvm:vm-9999-pvc-exist", Format: "raw", Size: 5 << 30}),
		fakeapi.WithVolume(storage.Volume{VolID: "local-lvm:vm-9999-pvc-exist-same-size", Format: "raw", Size: csi.MinChunkSizeBytes}),
		fakeapi.WithVolume(storage.Volume{VolID: "local-lvm:vm-9999-pvc-error", Format: "raw", Size: 1 << 30}),
		fakeapi.WithVolume(storage.Volume{VolID: "local-lvm:vm-9999-pvc-unpublished", Format: "raw", Size: 1 << 30}),
	)

	pve1.AddVM(100, &qemu.Config{
		Name: "cluster-1-node-1",
		SCSI: map[int]qemu.Drive{
			0: {File: "local-lvm:vm-100-disk-0", Size: "10G"},
			1: {File: "local-lvm:vm-9999-pvc-123", Backup: new(false), IOThread: new(true), WWN: "0x5056432d49443031"},
		},
		SMBios1: &qemu.SMBios1{UUID: "11833f4c-341f-4bd3-aad7-f7abed000000"},
	}, fakeapi.WithStatus(qemu.VMStatusRunning))

	pve2.AddVM(101, &qemu.Config{
		Name: "cluster-1-node-2",
		SCSI: map[int]qemu.Drive{
			0: {File: "local-lvm:vm-101-disk-0", Size: "10G"},
			1: {File: "local-lvm:vm-101-disk-1", Size: "1G"},
			2: {File: "rbd:9999/vm-9999-volume-rbd.raw", Backup: new(false), IOThread: new(true)},
			3: {File: "local-lvm:vm-101-disk-2", Size: "1G"},
		},
		SMBios1: &qemu.SMBios1{UUID: "11833f4c-341f-4bd3-aad7-f7abed000001"},
	}, fakeapi.WithStatus(qemu.VMStatusRunning))

	pool.SetProxmoxCluster("cluster-1", cl.Client(t))

	return cl
}
