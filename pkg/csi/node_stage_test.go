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
	"os"
	"path/filepath"
	"testing"
	"time"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/cloud-provider-openstack/pkg/util/mount"
	mountutil "k8s.io/mount-utils"
	"k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"
)

func writeDevice(t *testing.T, path string, size int) {
	t.Helper()

	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForDeviceReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	missing := filepath.Join(dir, "missing")

	empty := filepath.Join(dir, "empty")
	writeDevice(t, empty, 0)

	short := filepath.Join(dir, "short")
	writeDevice(t, short, 100)

	ready := filepath.Join(dir, "ready")
	writeDevice(t, ready, 4096)

	tests := []struct {
		msg           string
		devicePath    string
		expectedError string
	}{
		{
			msg:           "Missing",
			devicePath:    missing,
			expectedError: "failed to open device",
		},
		{
			msg:           "ZeroSize",
			devicePath:    empty,
			expectedError: "device size is zero",
		},
		{
			msg:           "ShortRead",
			devicePath:    short,
			expectedError: "failed to read first sector",
		},
		{
			msg:        "Ready",
			devicePath: ready,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.msg, func(t *testing.T) {
			t.Parallel()

			err := waitForDeviceReady(testCase.devicePath, 300*time.Millisecond)
			if testCase.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), testCase.devicePath)
				assert.Contains(t, err.Error(), testCase.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWaitForDeviceReadyBecomesReady(t *testing.T) {
	t.Parallel()

	devicePath := filepath.Join(t.TempDir(), "sdx")
	writeDevice(t, devicePath, 0)

	go func() {
		time.Sleep(300 * time.Millisecond)

		assert.NoError(t, os.WriteFile(devicePath, make([]byte, 4096), 0o600))
	}()

	assert.NoError(t, waitForDeviceReady(devicePath, 5*time.Second))
}

func newNodeStageTestService(m mount.IMount) *NodeService {
	return &NodeService{
		nodeID:             "fake-proxmox-node",
		Mount:              m,
		deviceReadyTimeout: 300 * time.Millisecond,
	}
}

func newNodeStageTestRequest(devicePath string, secrets map[string]string) *proto.NodeStageVolumeRequest {
	return &proto.NodeStageVolumeRequest{
		VolumeId:          "region/zone/storage/vm-9999-pvc-1",
		StagingTargetPath: "/staging",
		VolumeCapability: &proto.VolumeCapability{
			AccessMode: &proto.VolumeCapability_AccessMode{
				Mode: proto.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
			AccessType: &proto.VolumeCapability_Mount{
				Mount: &proto.VolumeCapability_MountVolume{
					FsType: "ext4",
				},
			},
		},
		PublishContext: map[string]string{
			"DevicePath": devicePath,
		},
		Secrets: secrets,
	}
}

func newNodeStageTestMounter() (*mount.MountMock, *testingexec.FakeExec) {
	fakeExec := &testingexec.FakeExec{ExactOrder: true}

	m := &mount.MountMock{}
	m.SetMounter(&mountutil.SafeFormatAndMount{
		Interface: mount.NewFakeMounter(),
		Exec:      exec.Interface(fakeExec),
	})

	return m, fakeExec
}

func TestNodeStageVolumeDeviceNotReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	empty := filepath.Join(dir, "empty")
	writeDevice(t, empty, 0)

	short := filepath.Join(dir, "short")
	writeDevice(t, short, 100)

	for _, devicePath := range []string{empty, short} {
		t.Run(filepath.Base(devicePath), func(t *testing.T) {
			t.Parallel()

			m, fakeExec := newNodeStageTestMounter()
			service := newNodeStageTestService(m)

			resp, err := service.NodeStageVolume(t.Context(), newNodeStageTestRequest(devicePath, nil))
			assert.Nil(t, resp)
			assert.Equal(t, codes.Unavailable, status.Code(err))
			assert.Contains(t, err.Error(), "is not ready")

			m.AssertNotCalled(t, "IsLikelyNotMountPointAttach", mock.Anything)
			assert.Equal(t, 0, fakeExec.CommandCalls, "blkid/mkfs must not run on a device that is not ready")
		})
	}
}

func TestNodeStageVolumeDeviceGoneAfterReady(t *testing.T) {
	t.Parallel()

	devicePath := filepath.Join(t.TempDir(), "sdx")
	writeDevice(t, devicePath, 4096)

	m, fakeExec := newNodeStageTestMounter()
	m.On("IsLikelyNotMountPointAttach", "/staging").Run(func(_ mock.Arguments) {
		// The device becomes unreadable after the first readiness check passed.
		assert.NoError(t, os.Truncate(devicePath, 0))
	}).Return(true, nil)

	service := newNodeStageTestService(m)

	resp, err := service.NodeStageVolume(t.Context(), newNodeStageTestRequest(devicePath, nil))
	assert.Nil(t, resp)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Contains(t, err.Error(), "is not ready")

	m.AssertExpectations(t)
	assert.Equal(t, 0, fakeExec.CommandCalls, "blkid/mkfs must not run on a device that is not ready")
}

func TestNodeStageVolumeDeviceReady(t *testing.T) {
	t.Parallel()

	devicePath := filepath.Join(t.TempDir(), "sdx")
	writeDevice(t, devicePath, 4096)

	m, fakeExec := newNodeStageTestMounter()
	m.On("IsLikelyNotMountPointAttach", "/staging").Return(false, nil)

	service := newNodeStageTestService(m)

	resp, err := service.NodeStageVolume(t.Context(), newNodeStageTestRequest(devicePath, nil))
	assert.NoError(t, err)
	assert.Equal(t, &proto.NodeStageVolumeResponse{}, resp)

	m.AssertExpectations(t)
	assert.Equal(t, 0, fakeExec.CommandCalls)
}

func TestNodeStageVolumeEncryptedProbeError(t *testing.T) {
	t.Parallel()

	// Readable, but too small for the filesystem probe to read all superblock offsets.
	devicePath := filepath.Join(t.TempDir(), "sdx")
	writeDevice(t, devicePath, 4096)

	m, fakeExec := newNodeStageTestMounter()
	m.On("IsLikelyNotMountPointAttach", "/staging").Return(true, nil)

	service := newNodeStageTestService(m)

	resp, err := service.NodeStageVolume(t.Context(), newNodeStageTestRequest(devicePath, map[string]string{
		EncryptionPassphraseKey: "secret",
	}))
	assert.Nil(t, resp)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "failed to probe filesystem")

	m.AssertExpectations(t)
	assert.Equal(t, 0, fakeExec.CommandCalls, "cryptsetup/blkid/mkfs must not run when the probe failed")
}
