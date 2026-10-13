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
	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
)

// storageHasVolume reports whether the fake Proxmox node lists volid in the given storage.
func (ts *configuredTestSuite) storageHasVolume(node, storageID, volid string) bool {
	ts.T().Helper()

	contents, err := ts.fake.Client(ts.T()).Nodes(node).Storage().Content(storageID).List(ts.T().Context(), nil)
	ts.Require().NoError(err)

	for _, c := range contents {
		if c.VolID == volid {
			return true
		}
	}

	return false
}

// contentSource returns a volume or snapshot content source for sourceID.
func contentSource(sourceID string, fromSnapshot bool) *proto.VolumeContentSource {
	if fromSnapshot {
		return &proto.VolumeContentSource{
			Type: &proto.VolumeContentSource_Snapshot{
				Snapshot: &proto.VolumeContentSource_SnapshotSource{SnapshotId: sourceID},
			},
		}
	}

	return &proto.VolumeContentSource{
		Type: &proto.VolumeContentSource_Volume{
			Volume: &proto.VolumeContentSource_VolumeSource{VolumeId: sourceID},
		},
	}
}

// Cloning a PVC on shared storage (Ceph RBD here). On real Proxmox, copying a volume
// within the same shared storage returns without copying anything
// (PVE::Storage::storage_migrate), so the clone can never be created. The fake API
// does copy it, so these tests check that the controller refuses before calling it.
func (ts *configuredTestSuite) TestCreateVolumeCloneOnSharedStorage() {
	volcap := &proto.VolumeCapability{
		AccessMode: &proto.VolumeCapability_AccessMode{
			Mode: proto.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		AccessType: &proto.VolumeCapability_Mount{
			Mount: &proto.VolumeCapability_MountVolume{
				FsType: "ext4",
			},
		},
	}

	tests := []struct {
		msg           string
		name          string
		storage       string
		sourceID      string
		fromSnapshot  bool
		expectedError error
	}{
		{
			msg:           "SharedStorage",
			name:          "pvc-ceph-clone",
			storage:       "ceph",
			sourceID:      "cluster-1//ceph/vm-9999-pvc-ceph-source",
			expectedError: status.Error(codes.InvalidArgument, "creating a volume from cluster-1//ceph/vm-9999-pvc-ceph-source is not supported: Proxmox cannot copy volumes within shared storage ceph"),
		},
		{
			// Volume IDs created before shared IDs dropped the zone still carry a node.
			msg:           "SharedStorageLegacyVolumeID",
			name:          "pvc-ceph-clone-legacy",
			storage:       "ceph",
			sourceID:      "cluster-1/pve-1/ceph/vm-9999-pvc-ceph-source",
			expectedError: status.Error(codes.InvalidArgument, "creating a volume from cluster-1/pve-1/ceph/vm-9999-pvc-ceph-source is not supported: Proxmox cannot copy volumes within shared storage ceph"),
		},
		{
			// Restoring a snapshot goes through the same copy.
			msg:           "SharedStorageFromSnapshot",
			name:          "pvc-ceph-restore",
			storage:       "ceph",
			sourceID:      "cluster-1//ceph/vm-9999-pvc-ceph-source",
			fromSnapshot:  true,
			expectedError: status.Error(codes.InvalidArgument, "creating a volume from cluster-1//ceph/vm-9999-pvc-ceph-source is not supported: Proxmox cannot copy volumes within shared storage ceph"),
		},
		{
			// A missing source is still reported as such.
			msg:           "SharedSourceNotFound",
			name:          "pvc-ceph-clone-missing",
			storage:       "ceph",
			sourceID:      "cluster-1//ceph/vm-9999-pvc-ceph-missing",
			expectedError: status.Error(codes.NotFound, "volume cluster-1//ceph/vm-9999-pvc-ceph-missing not found in any node for storage ceph"),
		},
	}

	for _, testCase := range tests {
		ts.Run(testCase.msg, func() {
			resp, err := ts.s.CreateVolume(ts.T().Context(), &proto.CreateVolumeRequest{
				Name:                testCase.name,
				Parameters:          map[string]string{"storage": testCase.storage},
				VolumeCapabilities:  []*proto.VolumeCapability{volcap},
				CapacityRange:       &proto.CapacityRange{RequiredBytes: 1 << 30},
				VolumeContentSource: contentSource(testCase.sourceID, testCase.fromSnapshot),
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
								corev1.LabelTopologyZone:   "pve-2",
							},
						},
					},
				},
			})
			ts.Require().Nil(resp)
			ts.Require().Equal(testCase.expectedError, err)

			// Nothing was allocated or copied on any node.
			volid := testCase.storage + ":vm-9999-" + testCase.name
			for _, node := range []string{"pve-1", "pve-2"} {
				ts.False(ts.storageHasVolume(node, testCase.storage, volid), "volume %s created on node %s", volid, node)
			}
		})
	}
}
