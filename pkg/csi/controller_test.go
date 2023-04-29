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
	"context"
	"fmt"
	"testing"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"

	corev1 "k8s.io/api/core/v1"
)

var _ proto.ControllerServer = (*csi.ControllerService)(nil)

type controllerServiceTestEnv struct {
	service *csi.ControllerService
}

func newControllerServerTestEnv() controllerServiceTestEnv {
	return controllerServiceTestEnv{
		service: &csi.ControllerService{},
	}
}

func TestNewControllerService(t *testing.T) {
	service, err := csi.NewControllerService("fake-file")

	assert.NotNil(t, err)
	assert.Nil(t, service)
	assert.Equal(t, "failed to read config: error reading fake-file: open fake-file: no such file or directory", err.Error())
}

func TestCreateVolume(t *testing.T) {
	t.Parallel()

	env := newControllerServerTestEnv()
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
	volParam := map[string]string{
		"storageID": "local-lvm",
	}
	volsize := &proto.CapacityRange{
		RequiredBytes: 1,
		LimitBytes:    1,
	}
	topology := &proto.TopologyRequirement{
		Preferred: []*proto.Topology{
			{
				Segments: map[string]string{
					corev1.LabelTopologyRegion: "region",
					corev1.LabelTopologyZone:   "zone",
				},
			},
		},
	}

	tests := []struct {
		msg           string
		request       *proto.CreateVolumeRequest
		expectedError error
	}{
		{
			msg: "EmptyVolumeName",
			request: &proto.CreateVolumeRequest{
				Name:                      "",
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				Parameters:                volParam,
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: fmt.Errorf("VolumeName must be provided"),
		},
		{
			msg: "VolumeCapabilities",
			request: &proto.CreateVolumeRequest{
				Name:                      "volume-id",
				Parameters:                volParam,
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: fmt.Errorf("VolumeCapabilities must be provided"),
		},
		{
			msg: "VolumeParameters",
			request: &proto.CreateVolumeRequest{
				Name:                      "volume-id",
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: fmt.Errorf("Parameters must be provided"),
		},
		{
			msg: "VolumeParametersStorege",
			request: &proto.CreateVolumeRequest{
				Name:                      "volume-id",
				Parameters:                map[string]string{},
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: fmt.Errorf("Parameters storageID must be provided"),
		},
		{
			msg: "RegionZone",
			request: &proto.CreateVolumeRequest{
				Name:               "volume-id",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
			},
			expectedError: fmt.Errorf("cannot find best region and zone"),
		},
		{
			msg: "EmptyZone",
			request: &proto.CreateVolumeRequest{
				Name:               "volume-id",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "region",
							},
						},
					},
				},
			},
			expectedError: fmt.Errorf("cannot find best region and zone"),
		},
		{
			msg: "EmptyRegion",
			request: &proto.CreateVolumeRequest{
				Name:               "volume-id",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyZone: "zone",
							},
						},
					},
				},
			},
			expectedError: fmt.Errorf("cannot find best region and zone"),
		},
		{
			msg: "WrongCluster",
			request: &proto.CreateVolumeRequest{
				Name:               "volume-id",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "fake-region",
								corev1.LabelTopologyZone:   "zone",
							},
						},
					},
				},
			},
			expectedError: fmt.Errorf("proxmox cluster fake-region not found"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.CreateVolume(context.Background(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

// nolint:dupl
func TestDeleteVolume(t *testing.T) {
	t.Parallel()

	env := newControllerServerTestEnv()

	tests := []struct {
		msg           string
		request       *proto.DeleteVolumeRequest
		expectedError error
	}{
		{
			msg:           "VolumeID",
			request:       &proto.DeleteVolumeRequest{},
			expectedError: fmt.Errorf("VolumeID must be provided"),
		},
		{
			msg: "VolumeID",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "volume-id",
			},
			expectedError: fmt.Errorf("VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "fake-region/node/data/volume-id",
			},
			expectedError: fmt.Errorf("proxmox cluster fake-region not found"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.DeleteVolume(context.Background(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

func TestControllerServiceControllerGetCapabilities(t *testing.T) {
	env := newControllerServerTestEnv()

	resp, err := env.service.ControllerGetCapabilities(context.Background(), &proto.ControllerGetCapabilitiesRequest{})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.GetCapabilities())

	if len(resp.Capabilities) != 5 {
		t.Fatalf("unexpected number of capabilities: %d", len(resp.Capabilities))
	}
}

// nolint:dupl
func TestControllerPublishVolumeError(t *testing.T) {
	t.Parallel()

	env := newControllerServerTestEnv()
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
	volCtx := map[string]string{}

	tests := []struct {
		msg           string
		request       *proto.ControllerPublishVolumeRequest
		expectedError error
	}{
		{
			msg: "VolumeID",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: fmt.Errorf("VolumeID must be provided"),
		},
		{
			msg: "NodeID",
			request: &proto.ControllerPublishVolumeRequest{
				VolumeId:         "volume-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: fmt.Errorf("NodeID must be provided"),
		},
		{
			msg: "VolumeCapability",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:        "node-id",
				VolumeId:      "volume-id",
				VolumeContext: volCtx,
			},
			expectedError: fmt.Errorf("VolumeCapability must be provided"),
		},
		{
			msg: "VolumeContext",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "volume-id",
				VolumeCapability: volcap,
			},
			expectedError: fmt.Errorf("VolumeContext must be provided"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "volume-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: fmt.Errorf("VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "fake-region/node/data/volume-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: fmt.Errorf("proxmox cluster fake-region not found"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.ControllerPublishVolume(context.Background(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

// nolint:dupl
func TestControllerUnpublishVolumeError(t *testing.T) {
	t.Parallel()

	env := newControllerServerTestEnv()

	tests := []struct {
		msg           string
		request       *proto.ControllerUnpublishVolumeRequest
		expectedError error
	}{
		{
			msg: "VolumeID",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId: "node-id",
			},
			expectedError: fmt.Errorf("VolumeID must be provided"),
		},
		{
			msg: "NodeID",
			request: &proto.ControllerUnpublishVolumeRequest{
				VolumeId: "volume-id",
			},
			expectedError: fmt.Errorf("NodeID must be provided"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "node-id",
				VolumeId: "volume-id",
			},
			expectedError: fmt.Errorf("VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "node-id",
				VolumeId: "fake-region/node/data/volume-id",
			},
			expectedError: fmt.Errorf("proxmox cluster fake-region not found"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.ControllerUnpublishVolume(context.Background(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

func TestValidateVolumeCapabilities(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.ValidateVolumeCapabilities(context.Background(), &proto.ValidateVolumeCapabilitiesRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.Unimplemented, ""), err)
}

func TestListVolumes(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.ListVolumes(context.Background(), &proto.ListVolumesRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.Unimplemented, ""), err)
}

func TestGetCapacity(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.GetCapacity(context.Background(), &proto.GetCapacityRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.InvalidArgument, "no topology specified"), err)
}

func TestCreateSnapshot(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.CreateSnapshot(context.Background(), &proto.CreateSnapshotRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.Unimplemented, ""), err)
}

func TestDeleteSnapshot(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.DeleteSnapshot(context.Background(), &proto.DeleteSnapshotRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.Unimplemented, ""), err)
}

func TestListSnapshots(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.ListSnapshots(context.Background(), &proto.ListSnapshotsRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.Unimplemented, ""), err)
}

func TestControllerExpandVolumeError(t *testing.T) {
	t.Parallel()

	env := newControllerServerTestEnv()

	capRange := &proto.CapacityRange{
		RequiredBytes: 100,
		LimitBytes:    150,
	}

	tests := []struct {
		msg           string
		request       *proto.ControllerExpandVolumeRequest
		expectedError error
	}{
		{
			msg: "VolumeID",
			request: &proto.ControllerExpandVolumeRequest{
				CapacityRange: capRange,
			},
			expectedError: fmt.Errorf("VolumeID must be provided"),
		},
		{
			msg: "CapacityRange",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId: "volume-id",
			},
			expectedError: fmt.Errorf("CapacityRange must be provided"),
		},
		{
			msg: "CapacityRangeLimit",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId: "volume-id",
				CapacityRange: &proto.CapacityRange{
					RequiredBytes: 150,
					LimitBytes:    100,
				},
			},
			expectedError: fmt.Errorf("after round-up, volume size exceeds the limit specified"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "volume-id",
				CapacityRange: capRange,
			},
			expectedError: fmt.Errorf("VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "fake-region/node/data/volume-id",
				CapacityRange: capRange,
			},
			expectedError: fmt.Errorf("proxmox cluster fake-region not found"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.ControllerExpandVolume(context.Background(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

func TestControllerGetVolume(t *testing.T) {
	env := newControllerServerTestEnv()

	_, err := env.service.ControllerGetVolume(context.Background(), &proto.ControllerGetVolumeRequest{})
	assert.NotNil(t, err)
	assert.Equal(t, status.Error(codes.Unimplemented, ""), err)
}
