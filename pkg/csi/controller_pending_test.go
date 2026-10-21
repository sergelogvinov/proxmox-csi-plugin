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
	"slices"
	"strings"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	testcluster "github.com/sergelogvinov/proxmox-csi-plugin/test/cluster"
)

// VM 100 (cluster-1-node-1 on pve-1) starts with scsi0 = its root disk and
// scsi1 = vm-9999-pvc-123, see testcluster.SetupFakeCluster.
const (
	pendingTestVMID   = 100
	pendingTestNodeID = "cluster-1-node-1"
	pendingTestVolume = "cluster-1/pve-1/local-lvm/vm-9999-pvc-unpublished"
)

func pendingTestPublishRequest() *proto.ControllerPublishVolumeRequest {
	return &proto.ControllerPublishVolumeRequest{
		NodeId:   pendingTestNodeID,
		VolumeId: pendingTestVolume,
		VolumeCapability: &proto.VolumeCapability{
			AccessMode: &proto.VolumeCapability_AccessMode{
				Mode: proto.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
			AccessType: &proto.VolumeCapability_Mount{
				Mount: &proto.VolumeCapability_MountVolume{FsType: "ext4"},
			},
		},
		VolumeContext: map[string]string{csi.StorageIDKey: "local-lvm"},
	}
}

// currentConfig reads the test VM's current (applied) config straight from the fake cluster.
func (ts *configuredTestSuite) currentConfig() *qemu.Config {
	cfg, err := ts.fake.Client(ts.T()).Nodes("pve-1").Qemu().Config(ts.T().Context(), pendingTestVMID, &qemu.ConfigOptions{Current: true})
	ts.Require().NoError(err)

	return cfg
}

func (ts *configuredTestSuite) TestControllerPublishVolumePendingOnlyIsNotAttached() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")

	// A previous hotplug of the volume at scsi2 failed: Proxmox kept it as pending only.
	pending.SetPending(pendingTestVMID, "scsi2", "local-lvm:vm-9999-pvc-unpublished,wwn=0x5000000000000002")

	resp, err := ts.s.ControllerPublishVolume(ts.T().Context(), pendingTestPublishRequest())
	ts.Require().NoError(err)

	// The failed hotplug is cleared first, which frees scsi2 for the attach.
	ts.Require().Equal("2", resp.GetPublishContext()["lun"])
	ts.Require().Contains(ts.currentConfig().SCSI[2].File, "vm-9999-pvc-unpublished")
	ts.Require().Equal([]int{2}, unpublishedLuns(ts.pendingConfig(pending)))
}

func (ts *configuredTestSuite) TestControllerPublishVolumeRetryAfterFailedHotplug() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")
	pending.FailHotplug(pendingTestVMID)

	_, err := ts.s.ControllerPublishVolume(ts.T().Context(), pendingTestPublishRequest())
	ts.Require().ErrorContains(err, "unable to attach disk")
	ts.Require().Equal(1, pending.Hotplugs(pendingTestVMID))

	// The retry sees the volume as pending only: it attaches again instead of reporting success.
	_, err = ts.s.ControllerPublishVolume(ts.T().Context(), pendingTestPublishRequest())
	ts.Require().ErrorContains(err, "unable to attach disk")
	ts.Require().Equal(2, pending.Hotplugs(pendingTestVMID))

	// Each retry reuses the slot of the failed hotplug, so the volume is never on two slots.
	ts.Require().Equal([]int{2}, unpublishedLuns(ts.pendingConfig(pending)))
	ts.Require().NotContains(ts.currentConfig().SCSI, 2)
}

func (ts *configuredTestSuite) TestControllerUnpublishVolumeCurrentAndPendingElsewhere() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")

	// The volume is current at scsi2, and a failed hotplug left it pending at scsi3 too.
	ts.attachCurrent(pendingTestVMID, "scsi2", "local-lvm:vm-9999-pvc-unpublished")
	pending.SetPending(pendingTestVMID, "scsi3", "local-lvm:vm-9999-pvc-unpublished")

	_, err := ts.s.ControllerUnpublishVolume(ts.T().Context(), &proto.ControllerUnpublishVolumeRequest{
		NodeId:   pendingTestNodeID,
		VolumeId: pendingTestVolume,
	})
	ts.Require().NoError(err)

	ts.Require().Empty(unpublishedLuns(ts.currentConfig()))
	ts.Require().Empty(unpublishedLuns(ts.pendingConfig(pending)))
	ts.Require().Contains(ts.currentConfig().SCSI[1].File, "vm-9999-pvc-123")
}

// unpublishedLuns returns the sorted SCSI luns of cfg that hold the test volume.
func unpublishedLuns(cfg *qemu.Config) []int {
	var luns []int

	for lun, drive := range cfg.SCSI {
		if strings.Contains(drive.File, "vm-9999-pvc-unpublished") {
			luns = append(luns, lun)
		}
	}

	slices.Sort(luns)

	return luns
}

// attachCurrent attaches file at drive straight in the fake cluster, so it is in the current config.
func (ts *configuredTestSuite) attachCurrent(vmid int, drive, file string) {
	cl := ts.fake.Client(ts.T())

	upid, err := cl.Nodes("pve-1").Qemu().AttachDrive(ts.T().Context(), vmid, &qemu.AttachDriveOptions{
		Drive:   drive,
		Options: qemu.Drive{File: file},
	})
	ts.Require().NoError(err)
	ts.Require().NoError(cl.Nodes("pve-1").Tasks().Wait(ts.T().Context(), upid, nil))
}

func (ts *configuredTestSuite) TestControllerPublishVolumeSkipsLunPendingDeletion() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")

	// A hot-unplug of an old volume at scsi2 failed: it is still current, pending deletion.
	ts.attachCurrent(pendingTestVMID, "scsi2", "local-lvm:vm-9999-pvc-exist")
	pending.DeletePending(pendingTestVMID, "scsi2")

	resp, err := ts.s.ControllerPublishVolume(ts.T().Context(), pendingTestPublishRequest())
	ts.Require().NoError(err)

	ts.Require().Equal("3", resp.GetPublishContext()["lun"])

	cfg := ts.currentConfig()
	ts.Require().Contains(cfg.SCSI[2].File, "vm-9999-pvc-exist")
	ts.Require().Contains(cfg.SCSI[3].File, "vm-9999-pvc-unpublished")
}

func (ts *configuredTestSuite) TestControllerUnpublishVolumePendingDeletionIsNotDetached() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")

	// A hot-unplug of vm-9999-pvc-123 at scsi1 failed, and keeps failing.
	pending.DeletePending(pendingTestVMID, "scsi1")
	pending.FailHotplug(pendingTestVMID)

	_, err := ts.s.ControllerUnpublishVolume(ts.T().Context(), &proto.ControllerUnpublishVolumeRequest{
		NodeId:   pendingTestNodeID,
		VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
	})
	ts.Require().Error(err)

	ts.Require().Contains(ts.currentConfig().SCSI[1].File, "vm-9999-pvc-123")
}

func (ts *configuredTestSuite) TestControllerExpandVolumePendingOnlyIsNotPublished() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")
	pending.SetPending(pendingTestVMID, "scsi2", "local-lvm:vm-9999-pvc-unpublished")

	_, err := ts.s.ControllerExpandVolume(ts.T().Context(), &proto.ControllerExpandVolumeRequest{
		VolumeId:      pendingTestVolume,
		CapacityRange: &proto.CapacityRange{RequiredBytes: 2 * csi.GiB},
	})
	ts.Require().Equal(status.Error(codes.Internal, "cannot resize unpublished"), err)
}

func (ts *configuredTestSuite) TestControllerPublishUnpublishVolumeNoPendingChanges() {
	testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")

	resp, err := ts.s.ControllerPublishVolume(ts.T().Context(), pendingTestPublishRequest())
	ts.Require().NoError(err)
	ts.Require().Equal("2", resp.GetPublishContext()["lun"])
	ts.Require().Contains(ts.currentConfig().SCSI[2].File, "vm-9999-pvc-unpublished")

	again, err := ts.s.ControllerPublishVolume(ts.T().Context(), pendingTestPublishRequest())
	ts.Require().NoError(err)
	ts.Require().Equal(resp.GetPublishContext(), again.GetPublishContext())

	_, err = ts.s.ControllerUnpublishVolume(ts.T().Context(), &proto.ControllerUnpublishVolumeRequest{
		NodeId:   pendingTestNodeID,
		VolumeId: pendingTestVolume,
	})
	ts.Require().NoError(err)

	ts.Require().NotContains(ts.currentConfig().SCSI, 2)
}

// pendingConfig reads the test VM's pending view (current with the pending changes applied) through the proxy.
func (ts *configuredTestSuite) pendingConfig(pending *testcluster.PendingConfig) *qemu.Config {
	cfg, err := pending.Client().Nodes("pve-1").Qemu().Config(ts.T().Context(), pendingTestVMID, nil)
	ts.Require().NoError(err)

	return cfg
}

func (ts *configuredTestSuite) TestControllerUnpublishVolumePendingOverOtherVolume() {
	pending := testcluster.SetupPendingConfig(ts.T(), ts.fake, ts.s.ProxmoxPool(), "cluster-1")

	// A hotplug of the volume at scsi1 failed while scsi1 holds vm-9999-pvc-123 in the current config.
	pending.SetPending(pendingTestVMID, "scsi1", "local-lvm:vm-9999-pvc-unpublished,wwn=0x5000000000000001")

	_, err := ts.s.ControllerUnpublishVolume(ts.T().Context(), &proto.ControllerUnpublishVolumeRequest{
		NodeId:   pendingTestNodeID,
		VolumeId: pendingTestVolume,
	})
	ts.Require().NoError(err)

	// The other volume keeps its slot, and the failed hotplug is gone from the pending view.
	ts.Require().Contains(ts.currentConfig().SCSI[1].File, "vm-9999-pvc-123")
	ts.Require().Contains(ts.pendingConfig(pending).SCSI[1].File, "vm-9999-pvc-123")
}
