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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/go-proxmox-rest/fakeapi"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
	toolsproxmox "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/proxmox"
	testcluster "github.com/sergelogvinov/proxmox-csi-plugin/test/cluster"
)

func newPendingCluster(t *testing.T) *testcluster.PendingConfig {
	t.Helper()

	cl := fakeapi.NewCluster(t, fakeapi.WithNodes("pve-1"))
	cl.Node("pve-1").AddVM(100, &qemu.Config{
		Name: "node-1",
		SCSI: map[int]qemu.Drive{
			0: {File: "local-lvm:vm-100-disk-0", Size: "10G"},
			1: {File: "local-lvm:vm-9999-pvc-123"},
		},
	}, fakeapi.WithStatus(qemu.VMStatusRunning))

	return testcluster.NewPendingConfig(t, cl)
}

func TestWaitForVolumeDetach(t *testing.T) {
	t.Parallel()

	t.Run("Detached", func(t *testing.T) {
		t.Parallel()

		pending := newPendingCluster(t)

		assert.NoError(t, toolsproxmox.WaitForVolumeDetach(t.Context(), pending.Client(), 100, "pvc-456"))
	})

	t.Run("PendingDeletion", func(t *testing.T) {
		t.Parallel()

		pending := newPendingCluster(t)
		pending.DeletePending(100, "scsi1")

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		assert.ErrorIs(t, toolsproxmox.WaitForVolumeDetach(ctx, pending.Client(), 100, "pvc-123"), context.DeadlineExceeded)
	})

	t.Run("PendingAddition", func(t *testing.T) {
		t.Parallel()

		pending := newPendingCluster(t)
		pending.SetPending(100, "scsi2", "local-lvm:vm-9999-pvc-456")

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		assert.ErrorIs(t, toolsproxmox.WaitForVolumeDetach(ctx, pending.Client(), 100, "pvc-456"), context.DeadlineExceeded)
	})
}
