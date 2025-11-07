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
	"maps"
	"testing"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	testcluster "github.com/sergelogvinov/proxmox-csi-plugin/test/cluster"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

var _ proto.ControllerServer = (*csi.ControllerService)(nil)

type baseCSITestSuite struct {
	suite.Suite

	s *csi.ControllerService
}

type configTestCase struct {
	name   string
	config string
}

func getTestConfigs() []configTestCase {
	return []configTestCase{
		{
			name:   "CapMoxProvider",
			config: "../../test/config/cluster-config-2.yaml",
		},
		{
			name:   "DefaultProvider",
			config: "../../test/config/cluster-config-1.yaml",
		},
	}
}

func (ts *baseCSITestSuite) setupTestSuite(config string) error {
	nodes := &corev1.NodeList{
		Items: []corev1.Node{
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1-node-1",
				},
				Spec: corev1.NodeSpec{
					ProviderID: "proxmox://cluster-1/100",
				},
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{
						SystemUUID: "11833f4c-341f-4bd3-aad7-f7abed000000",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-1-node-2",
				},
				Spec: corev1.NodeSpec{
					ProviderID: "proxmox://cluster-1/101",
				},
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{
						SystemUUID: "11833f4c-341f-4bd3-aad7-f7abed000001",
					},
				},
			},
		},
	}

	pv := &corev1.PersistentVolumeList{
		Items: []corev1.PersistentVolume{
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "PersistentVolume",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "pvc-123",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "PersistentVolume",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "pvc-123-lifecycle",
					Annotations: map[string]string{
						csi.PVAnnotationLifecycle: "keep",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "PersistentVolume",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "pvc-error",
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "PersistentVolume",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pvc-non-exist",
					Annotations: map[string]string{},
				},
			},
		},
	}

	kclient := fake.NewClientset(nodes, pv)

	px, err := csi.NewControllerService(kclient, config)
	if err != nil {
		return fmt.Errorf("failed to create controller service: %v", err)
	}

	ts.s = px
	ts.s.Init()

	return nil
}

// TestSuiteCSI runs all test configurations
func TestSuiteCSI(t *testing.T) {
	configs := getTestConfigs()
	for _, cfg := range configs {
		// Create a new test suite for each configuration
		ts := &baseCSITestSuite{}

		// Run the suite with the current configuration
		suite.Run(t, &configuredTestSuite{
			baseCSITestSuite: ts,
			configCase:       cfg,
		})
	}
}

// configuredTestSuite wraps the base suite with a specific configuration
type configuredTestSuite struct {
	*baseCSITestSuite

	configCase configTestCase
}

func (ts *configuredTestSuite) SetupTest() {
	testcluster.SetupMockResponders()

	err := ts.setupTestSuite(ts.configCase.config)
	if err != nil {
		ts.T().Fatalf("Failed to setup test suite: %v", err)
	}
}

func TestNewControllerService(t *testing.T) {
	service, err := csi.NewControllerService(&clientkubernetes.Clientset{}, "fake-file")
	assert.NotNil(t, err)
	assert.Nil(t, service)
	assert.Equal(t, "failed to read config: error reading fake-file: open fake-file: no such file or directory", err.Error())

	service, err = csi.NewControllerService(&clientkubernetes.Clientset{}, "../../hack/testdata/cloud-config.yaml")
	assert.Nil(t, err)
	assert.NotNil(t, service)
}

//nolint:dupl
func (ts *configuredTestSuite) TestCreateVolume() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

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
		"storage": "local-lvm",
	}
	volParamDefaults := map[string]string{
		"backup":    "0",
		"iothread":  "1",
		"storage":   "local-lvm",
		"replicate": "0",
	}
	volsize := &proto.CapacityRange{
		RequiredBytes: 1,
		LimitBytes:    100 * 1024 * 1024 * 1024,
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
		expected      *proto.CreateVolumeResponse
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
			expectedError: status.Error(codes.InvalidArgument, "VolumeName must be provided"),
		},
		{
			msg: "VolumeCapabilities",
			request: &proto.CreateVolumeRequest{
				Name:                      "volume-id",
				Parameters:                volParam,
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeCapabilities must be provided"),
		},
		{
			msg: "VolumeParametersStorage",
			request: &proto.CreateVolumeRequest{
				Name:                      "volume-id",
				Parameters:                map[string]string{},
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: status.Error(codes.InvalidArgument, "parameter storage must be provided"),
		},
		{
			msg: "VolumeParametersBlockSize",
			request: &proto.CreateVolumeRequest{
				Name: "volume-id",
				Parameters: map[string]string{
					"storage":   "local-lvm",
					"blockSize": "abc",
				},
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: status.Error(codes.InvalidArgument, "parameters blockSize must be a number"),
		},
		{
			msg: "VolumeParametersInodeSize",
			request: &proto.CreateVolumeRequest{
				Name: "volume-id",
				Parameters: map[string]string{
					"storage":   "local-lvm",
					"inodeSize": "abc",
				},
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: status.Error(codes.InvalidArgument, "parameters inodeSize must be a number"),
		},
		{
			msg: "RegionZone",
			request: &proto.CreateVolumeRequest{
				Name:               "volume-id",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
			},
			expectedError: status.Error(codes.Internal, "cannot find best region"),
		},
		{
			msg: "EmptyZone",
			request: &proto.CreateVolumeRequest{
				Name: "volume-id",
				Parameters: map[string]string{
					"storage": "fake-storage",
				},
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
							},
						},
					},
				},
			},
			expectedError: status.Error(codes.Internal, "cannot find best region and zone: storage fake-storage not found"),
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
			expectedError: status.Error(codes.Internal, "cannot find best region"),
		},
		{
			msg: "UnknownRegion",
			request: &proto.CreateVolumeRequest{
				Name:               "volume-id",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "unknown-region",
							},
						},
					},
				},
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "NonSupportZonalSMB",
			request: &proto.CreateVolumeRequest{
				Name: "volume-smb",
				Parameters: map[string]string{
					"storage": "smb",
				},
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
								corev1.LabelTopologyZone:   "pve-1",
							},
						},
					},
				},
			},
			expectedError: status.Error(codes.Internal, "error: shared storage type cifs, pbs are not supported"),
		},
		{
			msg: "SupportZonalRBD",
			request: &proto.CreateVolumeRequest{
				Name: "volume-rbd",
				Parameters: map[string]string{
					"storage": "rbd",
				},
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
							},
						},
					},
				},
			},
			expected: &proto.CreateVolumeResponse{
				Volume: &proto.Volume{
					VolumeId: "cluster-1//rbd/9999/vm-9999-volume-rbd.raw",
					VolumeContext: func() map[string]string {
						vc := maps.Clone(volParamDefaults)
						vc["storage"] = "rbd"

						return vc
					}(),
					CapacityBytes: csi.MinChunkSizeBytes,
					AccessibleTopology: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
							},
						},
					},
				},
			},
		},
		{
			msg: "PVCAlreadyExistSameSize",
			request: &proto.CreateVolumeRequest{
				Name:               "pvc-exist-same-size",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
								corev1.LabelTopologyZone:   "pve-1",
							},
						},
					},
				},
			},
			expected: &proto.CreateVolumeResponse{
				Volume: &proto.Volume{
					VolumeId:      "cluster-1/pve-1/local-lvm/vm-9999-pvc-exist-same-size",
					VolumeContext: volParamDefaults,
					CapacityBytes: csi.MinChunkSizeBytes,
					AccessibleTopology: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
								corev1.LabelTopologyZone:   "pve-1",
							},
						},
					},
				},
			},
		},
		{
			msg: "CreateVolume",
			request: &proto.CreateVolumeRequest{
				Name:               "pvc-123",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange:      volsize,
				AccessibilityRequirements: &proto.TopologyRequirement{
					Preferred: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
								corev1.LabelTopologyZone:   "pve-1",
							},
						},
					},
				},
			},
			expected: &proto.CreateVolumeResponse{
				Volume: &proto.Volume{
					VolumeId:      "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
					VolumeContext: volParamDefaults,
					CapacityBytes: csi.MinChunkSizeBytes,
					AccessibleTopology: []*proto.Topology{
						{
							Segments: map[string]string{
								corev1.LabelTopologyRegion: "cluster-1",
								corev1.LabelTopologyZone:   "pve-1",
							},
						},
					},
				},
			},
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.CreateVolume(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(resp, testCase.expected)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(err, testCase.expectedError)
			}
		})
	}
}

//nolint:dupl
func (ts *configuredTestSuite) TestDeleteVolume() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	tests := []struct {
		msg           string
		request       *proto.DeleteVolumeRequest
		expected      *proto.DeleteVolumeResponse
		expectedError error
	}{
		{
			msg: "VolumeID",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "volume-id",
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "fake-region/node/data/volume-id",
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "WrongPVZone",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "cluster-1/pve-removed/local-lvm/vm-9999-pvc-non-exist",
			},
			expected: &proto.DeleteVolumeResponse{},
		},
		{
			msg: "VolumeIDNonExist",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "cluster-1/pve-1/wrong-volume/vm-9999-pvc-non-exist",
			},
			expected: &proto.DeleteVolumeResponse{},
		},
		{
			msg: "PVCNonExist",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-non-exist",
			},
			expected: &proto.DeleteVolumeResponse{},
		},
		{
			msg: "DeleteVolume",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
			},
			expected: &proto.DeleteVolumeResponse{},
		},
		{
			msg: "DeleteVolumeError",
			request: &proto.DeleteVolumeRequest{
				VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-error",
			},
			expectedError: status.Error(codes.Internal, "failed to delete volume: cluster-1/pve-1/local-lvm/vm-9999-pvc-error, unable to delete virtual machine disk: ERROR"),
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.DeleteVolume(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(resp, testCase.expected)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError, err)
			}
		})
	}
}

func (ts *configuredTestSuite) TestControllerServiceControllerGetCapabilities() {
	resp, err := ts.s.ControllerGetCapabilities(context.Background(), &proto.ControllerGetCapabilitiesRequest{})
	ts.Require().NoError(err)
	ts.Require().NotNil(resp)

	if len(resp.GetCapabilities()) != 9 {
		ts.T().Fatalf("unexpected number of capabilities: %d", len(resp.GetCapabilities()))
	}
}

//nolint:dupl
func (ts *configuredTestSuite) TestControllerPublishVolumeError() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	volCap := &proto.VolumeCapability{
		AccessMode: &proto.VolumeCapability_AccessMode{
			Mode: proto.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		AccessType: &proto.VolumeCapability_Mount{
			Mount: &proto.VolumeCapability_MountVolume{
				FsType: "ext4",
			},
		},
	}
	volCtx := map[string]string{
		csi.StorageIDKey: "local-lvm",
	}

	tests := []struct {
		msg           string
		request       *proto.ControllerPublishVolumeRequest
		expected      *proto.ControllerPublishVolumeResponse
		expectedError error
	}{
		{
			msg: "NodeID",
			request: &proto.ControllerPublishVolumeRequest{
				VolumeId:         "volume-id",
				VolumeCapability: volCap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.InvalidArgument, "NodeID must be provided"),
		},
		{
			msg: "VolumeCapability",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:        "node-id",
				VolumeId:      "volume-id",
				VolumeContext: volCtx,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeCapability must be provided"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "volume-id",
				VolumeCapability: volCap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "fake-region/node-id/data/volume-id",
				VolumeCapability: volCap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		// {
		// 	msg: "WrongNode",
		// 	request: &proto.ControllerPublishVolumeRequest{
		// 		NodeId:           "cluster-1-node-2",
		// 		VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
		// 		VolumeCapability: volCap,
		// 		VolumeContext:    volCtx,
		// 		Readonly:         true,
		// 	},
		// 	expectedError: status.Error(codes.InvalidArgument, "volume cluster-1/pve-1/local-lvm/vm-9999-pvc-123 does not exist on the node cluster-1-node-2"),
		// },
		{
			msg: "VolumeNotExist",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "cluster-1-node-1",
				VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-123-not-exist",
				VolumeCapability: volCap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.NotFound, "volume cluster-1/pve-1/local-lvm/vm-9999-pvc-123-not-exist not found"),
		},
		{
			msg: "VolumeAlreadyAttached",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "cluster-1-node-1",
				VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
				VolumeCapability: volCap,
				VolumeContext:    volCtx,
			},
			expected: &proto.ControllerPublishVolumeResponse{
				PublishContext: map[string]string{
					"DevicePath": "/dev/disk/by-id/wwn-0x5056432d49443031",
					"lun":        "1",
				},
			},
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.ControllerPublishVolume(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(resp, testCase.expected)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError, err)
			}
		})
	}
}

//nolint:dupl
func (ts *configuredTestSuite) TestControllerUnpublishVolumeError() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	tests := []struct {
		msg           string
		request       *proto.ControllerUnpublishVolumeRequest
		expectedError error
	}{
		{
			msg: "NodeID",
			request: &proto.ControllerUnpublishVolumeRequest{
				VolumeId: "volume-id",
			},
			expectedError: status.Error(codes.InvalidArgument, "NodeID must be provided"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "node-id",
				VolumeId: "volume-id",
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "node-id",
				VolumeId: "fake-region/node/data/volume-id",
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "WrongNode",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "cluster-1-node-3",
				VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
			},
			expectedError: status.Error(codes.InvalidArgument, "nodes \"cluster-1-node-3\" not found"),
		},
		{
			msg: "WrongPVZone",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "cluster-1-node-2",
				VolumeId: "cluster-1/pve-removed/local-lvm/vm-9999-pvc-exist",
			},
		},
		{
			msg: "AlreadyDetached",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "cluster-1-node-2",
				VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
			},
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			_, err := ts.s.ControllerUnpublishVolume(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError.Error(), err.Error())
			}
		})
	}
}

func (ts *configuredTestSuite) TestValidateVolumeCapabilities() {
	_, err := ts.s.ValidateVolumeCapabilities(context.Background(), &proto.ValidateVolumeCapabilitiesRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *configuredTestSuite) TestListVolumes() {
	_, err := ts.s.ListVolumes(context.Background(), &proto.ListVolumesRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *configuredTestSuite) TestGetCapacity() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	tests := []struct {
		msg           string
		request       *proto.GetCapacityRequest
		expected      *proto.GetCapacityResponse
		expectedError error
	}{
		{
			msg:           "NoTopology",
			request:       &proto.GetCapacityRequest{},
			expectedError: status.Error(codes.InvalidArgument, "no topology specified"),
		},
		{
			msg: "NoTopology",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{},
			},
			expectedError: status.Error(codes.InvalidArgument, "region and storage must be provided"),
		},
		{
			msg: "TopologyRegion",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
					},
				},
				Parameters: map[string]string{
					csi.StorageIDKey: "local-lvm",
				},
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "TopologyZone",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyZone: "zone",
					},
				},
				Parameters: map[string]string{
					csi.StorageIDKey: "local-lvm",
				},
			},
			expectedError: status.Error(codes.InvalidArgument, "region and storage must be provided"),
		},
		{
			msg: "TopologyStorageName",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
			},
			expectedError: status.Error(codes.InvalidArgument, "region and storage must be provided"),
		},
		{
			msg: "Topology",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
				Parameters: map[string]string{
					csi.StorageIDKey: "local-lvm",
				},
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "StorageNotExists",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "cluster-1",
						corev1.LabelTopologyZone:   "pve-1",
					},
				},
				Parameters: map[string]string{
					csi.StorageIDKey: "storage",
				},
			},
			expectedError: status.Error(codes.Internal, "not found"),
		},
		{
			msg: "Storage",
			request: &proto.GetCapacityRequest{
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "cluster-1",
						corev1.LabelTopologyZone:   "pve-1",
					},
				},
				Parameters: map[string]string{
					csi.StorageIDKey: "local-lvm",
				},
			},
			expected: &proto.GetCapacityResponse{
				AvailableCapacity: 50 * 1024 * 1024 * 1024,
			},
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.GetCapacity(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(testCase.expected, resp)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError, err)
			}
		})
	}
}

func (ts *configuredTestSuite) TestCreateSnapshot() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	tests := []struct {
		msg           string
		request       *proto.CreateSnapshotRequest
		expected      *proto.CreateSnapshotResponse
		expectedError error
	}{
		{
			msg:           "VolumeID",
			request:       &proto.CreateSnapshotRequest{},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.CreateSnapshotRequest{
				Name: "name",
				Parameters: map[string]string{
					"param": "value",
				},
				SourceVolumeId: "fake-region/node/data/volume-id",
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.CreateSnapshot(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(testCase.expected, resp)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError, err)
			}
		})
	}
}

func (ts *configuredTestSuite) TestDeleteSnapshot() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	tests := []struct {
		msg           string
		request       *proto.DeleteSnapshotRequest
		expected      *proto.DeleteSnapshotResponse
		expectedError error
	}{
		{
			msg:           "VolumeID",
			request:       &proto.DeleteSnapshotRequest{},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.DeleteSnapshotRequest{
				SnapshotId: "fake-region/node/data/volume-id",
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "PVCNonExist",
			request: &proto.DeleteSnapshotRequest{
				SnapshotId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-none",
			},
			expected: &proto.DeleteSnapshotResponse{},
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.DeleteSnapshot(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(testCase.expected, resp)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError, err)
			}
		})
	}
}

func (ts *configuredTestSuite) TestListSnapshots() {
	_, err := ts.s.ListSnapshots(context.Background(), &proto.ListSnapshotsRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *configuredTestSuite) TestControllerExpandVolumeError() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset() //nolint: wsl_v5

	capRange := &proto.CapacityRange{
		RequiredBytes: 100 * csi.GiB,
		LimitBytes:    150 * csi.GiB,
	}

	tests := []struct {
		msg           string
		request       *proto.ControllerExpandVolumeRequest
		expected      *proto.ControllerExpandVolumeResponse
		expectedError error
	}{
		{
			msg: "VolumeID",
			request: &proto.ControllerExpandVolumeRequest{
				CapacityRange: capRange,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "CapacityRange",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId: "volume-id",
			},
			expectedError: status.Error(codes.InvalidArgument, "CapacityRange must be provided"),
		},
		{
			msg: "CapacityRangeLimit",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId: "volume-id",
				CapacityRange: &proto.CapacityRange{
					RequiredBytes: 150 * csi.GiB,
					LimitBytes:    100 * csi.GiB,
				},
			},
			expectedError: status.Error(codes.OutOfRange, "after round-up, volume size exceeds the limit specified"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "fake-region/node/data/volume-id",
				CapacityRange: capRange,
			},
			expectedError: status.Error(codes.Internal, "region not found"),
		},
		{
			msg: "WrongPVC",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "cluster-1/pve-1/local-lvm/vm-9999-pvc-none",
				CapacityRange: capRange,
			},
			expectedError: status.Error(codes.NotFound, "volume cluster-1/pve-1/local-lvm/vm-9999-pvc-none not found"),
		},
		{
			msg: "WrongPVZone",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "cluster-1/pve-removed/local-lvm/vm-9999-pvc-exist",
				CapacityRange: capRange,
			},
			expectedError: status.Error(codes.NotFound, "zone pve-removed not found in cluster cluster-1"),
		},
		{
			msg: "UnpublishedVolume",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "cluster-1/pve-1/local-lvm/vm-9999-pvc-unpublished",
				CapacityRange: capRange,
			},
			expectedError: status.Error(codes.Internal, "cannot resize unpublished"),
		},
		{
			msg: "ExpandVolume",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:      "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
				CapacityRange: capRange,
			},
			expected: &proto.ControllerExpandVolumeResponse{
				CapacityBytes:         100 * csi.GiB,
				NodeExpansionRequired: true,
			},
		},
	}

	for _, testCase := range tests {
		ts.Run(fmt.Sprint(testCase.msg), func() {
			resp, err := ts.s.ControllerExpandVolume(context.Background(), testCase.request)
			if testCase.expectedError == nil {
				ts.Require().NoError(err)
				ts.Require().Equal(testCase.expected, resp)
			} else {
				ts.Require().Error(err)
				ts.Require().Equal(testCase.expectedError, err)
			}
		})
	}
}

func (ts *configuredTestSuite) TestControllerGetVolume() {
	_, err := ts.s.ControllerGetVolume(context.Background(), &proto.ControllerGetVolumeRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}
