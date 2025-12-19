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
	"fmt"
	"net/http"

	"github.com/jarcoal/httpmock"
	"github.com/luthermonson/go-proxmox"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
)

// SetupMockResponders sets up the HTTP mock responders for Proxmox API calls.
func SetupMockResponders() {
	httpmock.RegisterResponder(http.MethodGet, `=~/version$`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Version{Version: "8.4"},
			})
		})
	httpmock.RegisterResponder(http.MethodGet, `=~/cluster/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.NodeStatuses{{Name: "pve-1"}, {Name: "pve-2"}, {Name: "pve-3"}},
			})
		})
	httpmock.RegisterResponder(http.MethodGet, "=~/cluster/resources",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.ClusterResources{
					&proxmox.ClusterResource{
						Node:   "pve-1",
						Type:   "qemu",
						VMID:   100,
						Name:   "cluster-1-node-1",
						MaxCPU: 4,
						MaxMem: 10 * 1024 * 1024 * 1024,
					},
					&proxmox.ClusterResource{
						Node:   "pve-2",
						Type:   "qemu",
						VMID:   101,
						Name:   "cluster-1-node-2",
						MaxCPU: 2,
						MaxMem: 5 * 1024 * 1024 * 1024,
					},

					&proxmox.ClusterResource{
						ID:         "storage/smb",
						Type:       "storage",
						PluginType: "cifs",
						Node:       "pve-1",
						Storage:    "smb",
						Content:    "rootdir,images",
						Shared:     1,
						Status:     "available",
					},
					&proxmox.ClusterResource{
						ID:         "storage/rbd",
						Type:       "storage",
						PluginType: "dir",
						Node:       "pve-1",
						Storage:    "rbd",
						Content:    "images",
						Shared:     1,
						Status:     "available",
					},
					&proxmox.ClusterResource{
						ID:         "storage/zfs",
						Type:       "storage",
						PluginType: "zfspool",
						Node:       "pve-1",
						Storage:    "zfs",
						Content:    "images",
						Status:     "available",
					},
					&proxmox.ClusterResource{
						ID:         "storage/zfs",
						Type:       "storage",
						PluginType: "zfspool",
						Node:       "pve-2",
						Storage:    "zfs",
						Content:    "images",
						Status:     "available",
					},
					&proxmox.ClusterResource{
						ID:         "storage/lvm",
						Type:       "storage",
						PluginType: "lvm",
						Node:       "pve-1",
						Storage:    "local-lvm",
						Content:    "images",
						Status:     "available",
					},
					&proxmox.ClusterResource{
						ID:         "storage/lvm",
						Type:       "storage",
						PluginType: "lvm",
						Node:       "pve-2",
						Storage:    "local-lvm",
						Content:    "images",
						Status:     "available",
					},
				},
			})
		},
	)

	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/pve-1/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Node{},
			})
		})
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/pve-2/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Node{},
			})
		})
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/pve-3/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Node{},
			})
		})

	httpmock.RegisterResponder(http.MethodGet, "=~/nodes$",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": []proxmox.NodeStatus{
					{
						Node:   "pve-1",
						Status: "online",
					},
					{
						Node:   "pve-2",
						Status: "online",
					},
					{
						Node:   "pve-3",
						Status: "online",
					},
				},
			})
		})

	httpmock.RegisterResponder(http.MethodGet, `=~/storage/rbd$`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.ClusterStorage{
					Type:    "dir",
					Storage: "rbd",
					Shared:  1,
					Content: "images",
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/rbd/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Storage{
					Type:    "dir",
					Enabled: 1,
					Active:  1,
					Shared:  1,
					Content: "images",
					Total:   100 * 1024 * 1024 * 1024,
					Used:    50 * 1024 * 1024 * 1024,
					Avail:   50 * 1024 * 1024 * 1024,
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/zfs/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Storage{
					Type:    "zfspool",
					Enabled: 1,
					Active:  1,
					Content: "images",
					Total:   100 * 1024 * 1024 * 1024,
					Used:    50 * 1024 * 1024 * 1024,
					Avail:   50 * 1024 * 1024 * 1024,
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/local-lvm/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.Storage{
					Type:    "lvmthin",
					Enabled: 1,
					Active:  1,
					Content: "images",
					Total:   100 * 1024 * 1024 * 1024,
					Used:    50 * 1024 * 1024 * 1024,
					Avail:   50 * 1024 * 1024 * 1024,
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/\S+/status`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(400, map[string]any{
				"data":    nil,
				"message": "Parameter verification failed",
				"errors": map[string]string{
					"storage": "No such storage.",
				},
			})
		},
	)

	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/smb/content`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": []proxmox.StorageContent{
					{
						Format: "raw",
						Volid:  "smb:9999/vm-9999-volume-smb.raw",
						VMID:   9999,
						Size:   1024 * 1024 * 1024,
					},
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/rbd/content`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": []proxmox.StorageContent{
					{
						Format: "raw",
						Volid:  "rbd:9999/vm-9999-volume-rbd.raw",
						VMID:   9999,
						Size:   1024 * 1024 * 1024,
					},
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/storage/local-lvm/content`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": []proxmox.StorageContent{
					{
						Format: "raw",
						Size:   uint64(csi.MinChunkSizeBytes),
						Volid:  "local-lvm:vm-9999-pvc-123",
					},
					{
						Format: "raw",
						Size:   5 * 1024 * 1024 * 1024,
						Volid:  "local-lvm:vm-9999-pvc-exist",
					},
					{
						Format: "raw",
						Size:   uint64(csi.MinChunkSizeBytes),
						Volid:  "local-lvm:vm-9999-pvc-exist-same-size",
					},
					{
						Format: "raw",
						Size:   1024 * 1024 * 1024,
						Volid:  "local-lvm:vm-9999-pvc-error",
					},
					{
						Format: "raw",
						Size:   1024 * 1024 * 1024,
						Volid:  "local-lvm:vm-9999-pvc-unpublished",
					},
				},
			})
		},
	)

	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/pve-1/qemu$`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": []proxmox.VirtualMachine{
					{
						VMID:   100,
						Status: "running",
						Name:   "cluster-1-node-1",
						Node:   "pve-1",
					},
				},
			})
		})
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/pve-2/qemu$`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": []proxmox.VirtualMachine{
					{
						VMID:   101,
						Status: "running",
						Name:   "cluster-1-node-2",
						Node:   "pve-2",
					},
				},
			})
		})
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/qemu/100/status/current`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.VirtualMachine{
					VMID:   100,
					Name:   "cluster-1-node-1",
					Node:   "pve-1",
					Status: "running",
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/qemu/100/config`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": map[string]interface{}{
					"vmid":    100,
					"scsi0":   "local-lvm:vm-100-disk-0,size=10G",
					"scsi1":   "local-lvm:vm-9999-pvc-123,backup=0,iothread=1,wwn=0x5056432d49443031",
					"smbios1": "uuid=11833f4c-341f-4bd3-aad7-f7abed000000",
				},
			})
		},
	)

	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/qemu/101/status/current`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": proxmox.VirtualMachine{
					VMID:   101,
					Name:   "cluster-1-node-2",
					Node:   "pve-2",
					Status: "running",
				},
			})
		},
	)
	httpmock.RegisterResponder(http.MethodGet, `=~/nodes/\S+/qemu/101/config`,
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"vmid":    101,
					"scsi0":   "local-lvm:vm-101-disk-0,size=10G",
					"scsi1":   "local-lvm:vm-101-disk-1,size=1G",
					"scsi3":   "local-lvm:vm-101-disk-2,size=1G",
					"smbios1": "uuid=11833f4c-341f-4bd3-aad7-f7abed000001",
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.2:8006/api2/json/nodes/pve-3/qemu/100/config",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"vmid":    100,
					"smbios1": "uuid=11833f4c-341f-4bd3-aad7-f7abea000000",
				},
			})
		},
	)

	httpmock.RegisterResponder("PUT", "https://127.0.0.1:8006/api2/json/nodes/pve-1/qemu/100/resize",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]any{
				"data": "",
			})
		},
	)

	task := &proxmox.Task{
		UPID:      "UPID:pve-1:003B4235:1DF4ABCA:667C1C45:csi:103:root@pam:",
		Type:      "delete",
		User:      "root",
		Status:    "completed",
		Node:      "pve-1",
		IsRunning: false,
	}

	taskErr := &proxmox.Task{
		UPID:       "UPID:pve-1:003B4235:1DF4ABCA:667C1C45:csi:104:root@pam:",
		Type:       "delete",
		User:       "root",
		Status:     "stopped",
		ExitStatus: "ERROR",
		Node:       "pve-1",
		IsRunning:  false,
	}

	httpmock.RegisterResponder(http.MethodGet, fmt.Sprintf(`=~/nodes/%s/tasks/%s/status`, "pve-1", string(task.UPID)),
		httpmock.NewJsonResponderOrPanic(200, map[string]any{"data": task}))
	httpmock.RegisterResponder(http.MethodGet, fmt.Sprintf(`=~/nodes/%s/tasks/%s/status`, "pve-1", string(taskErr.UPID)),
		httpmock.NewJsonResponderOrPanic(200, map[string]any{"data": taskErr}))

	httpmock.RegisterResponder(http.MethodDelete, `=~/nodes/pve-1/storage/local-lvm/content/vm-9999-pvc-123`,
		httpmock.NewJsonResponderOrPanic(200, map[string]any{"data": task.UPID}).Times(1))
	httpmock.RegisterResponder(http.MethodDelete, `=~/nodes/pve-1/storage/local-lvm/content/vm-9999-pvc-error`,
		httpmock.NewJsonResponderOrPanic(200, map[string]any{"data": taskErr.UPID}).Times(1))
}
