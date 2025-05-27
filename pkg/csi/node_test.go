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

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

var _ proto.NodeServer = (*csi.NodeService)(nil)

type nodeServiceTestEnv struct {
	service *csi.NodeService
}

func newNodeServerTestEnv() nodeServiceTestEnv {
	return nodeServiceTestEnv{
		service: csi.NewNodeService("fake-proxmox-node", nil),
	}
}

func TestNodeStageVolumeErrors(t *testing.T) {
	t.Parallel()

	env := newNodeServerTestEnv()
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

	params := map[string]string{
		"DevicePath": "/dev/zero",
	}

	tests := []struct {
		msg           string
		request       *proto.NodeStageVolumeRequest
		expectedError error
	}{
		{
			msg: "VolumePath",
			request: &proto.NodeStageVolumeRequest{
				StagingTargetPath: "/staging",
				VolumeCapability:  volcap,
				PublishContext:    params,
			},
			expectedError: fmt.Errorf("VolumeID must be provided"),
		},
		{
			msg: "StagingTargetPath",
			request: &proto.NodeStageVolumeRequest{
				VolumeId:         "pvc-1",
				VolumeCapability: volcap,
				PublishContext:   params,
			},
			expectedError: fmt.Errorf("StagingTargetPath must be provided"),
		},
		{
			msg: "VolumeCapability",
			request: &proto.NodeStageVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				PublishContext:    params,
			},
			expectedError: fmt.Errorf("VolumeCapability must be provided"),
		},
		{
			msg: "BlockVolume",
			request: &proto.NodeStageVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				VolumeCapability: &proto.VolumeCapability{
					AccessMode: &proto.VolumeCapability_AccessMode{
						Mode: proto.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
					AccessType: &proto.VolumeCapability_Block{
						Block: &proto.VolumeCapability_BlockVolume{},
					},
				},
				PublishContext: params,
			},
			expectedError: nil,
		},

		{
			msg: "DevicePath",
			request: &proto.NodeStageVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				VolumeCapability:  volcap,
				PublishContext:    map[string]string{},
			},
			expectedError: fmt.Errorf("DevicePath must be provided"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			resp, err := env.service.NodeStageVolume(t.Context(), testCase.request)

			if testCase.expectedError != nil {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), testCase.expectedError.Error())
			} else {
				assert.Nil(t, err)
				assert.NotNil(t, resp)
				assert.Equal(t, resp, &proto.NodeStageVolumeResponse{})
			}
		})
	}
}

//nolint:dupl
func TestNodeUnstageVolumeErrors(t *testing.T) {
	t.Parallel()

	env := newNodeServerTestEnv()
	tests := []struct {
		msg           string
		request       *proto.NodeUnstageVolumeRequest
		expectedError error
	}{
		{
			msg: "VolumePath",
			request: &proto.NodeUnstageVolumeRequest{
				VolumeId: "pvc-1",
			},
			expectedError: fmt.Errorf("StagingTargetPath must be provided"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.NodeUnstageVolume(t.Context(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

func TestNodeServiceNodePublishVolumeErrors(t *testing.T) {
	t.Parallel()

	env := newNodeServerTestEnv()
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

	params := map[string]string{
		"DevicePath": "/dev/zero",
	}

	tests := []struct {
		msg           string
		request       *proto.NodePublishVolumeRequest
		expectedError error
	}{
		{
			msg: "StagingTargetPath",
			request: &proto.NodePublishVolumeRequest{
				VolumeId:         "pvc-1",
				TargetPath:       "/target",
				VolumeCapability: volcap,
				PublishContext:   params,
			},
			expectedError: fmt.Errorf("StagingTargetPath must be provided"),
		},
		{
			msg: "TargetPath",
			request: &proto.NodePublishVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				VolumeCapability:  volcap,
				PublishContext:    params,
			},
			expectedError: fmt.Errorf("TargetPath must be provided"),
		},
		{
			msg: "VolumeCapability",
			request: &proto.NodePublishVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				TargetPath:        "/target",
				PublishContext:    params,
			},
			expectedError: fmt.Errorf("VolumeCapability must be provided"),
		},
		{
			msg: "VolumeCapability",
			request: &proto.NodePublishVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				TargetPath:        "/target",
				VolumeCapability: &proto.VolumeCapability{
					AccessMode: &proto.VolumeCapability_AccessMode{
						Mode: proto.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
				PublishContext: params,
			},
			expectedError: fmt.Errorf("VolumeCapability not supported"),
		},
		{
			msg: "VolumeCapability",
			request: &proto.NodePublishVolumeRequest{
				VolumeId:          "pvc-1",
				StagingTargetPath: "/staging",
				TargetPath:        "/target",
				VolumeCapability:  volcap,
				PublishContext:    map[string]string{},
			},
			expectedError: fmt.Errorf("DevicePath must be provided"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.NodePublishVolume(t.Context(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

//nolint:dupl
func TestNodeUnpublishVolumeErrors(t *testing.T) {
	t.Parallel()

	env := newNodeServerTestEnv()
	tests := []struct {
		msg           string
		request       *proto.NodeUnpublishVolumeRequest
		expectedError error
	}{
		{
			msg:           "EmptyRequest",
			request:       &proto.NodeUnpublishVolumeRequest{},
			expectedError: fmt.Errorf("TargetPath must be provided"),
		},
		{
			msg: "TargetPath",
			request: &proto.NodeUnpublishVolumeRequest{
				VolumeId: "pvc-1",
			},
			expectedError: fmt.Errorf("TargetPath must be provided"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.NodeUnpublishVolume(t.Context(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

//nolint:dupl
func TestNodeGetVolumeStatsErrors(t *testing.T) {
	t.Parallel()

	env := newNodeServerTestEnv()
	tests := []struct {
		msg           string
		request       *proto.NodeGetVolumeStatsRequest
		expectedError error
	}{
		{
			msg: "VolumePath",
			request: &proto.NodeGetVolumeStatsRequest{
				VolumeId: "pvc-1",
			},
			expectedError: fmt.Errorf("VolumePath must be provided"),
		},
		{
			msg: "VolumePath",
			request: &proto.NodeGetVolumeStatsRequest{
				VolumeId:   "pvc-1",
				VolumePath: "/some-test-path",
			},
			expectedError: fmt.Errorf("target: /some-test-path not found"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.NodeGetVolumeStats(t.Context(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

func TestNodeServiceNodeExpandVolumeErrors(t *testing.T) {
	t.Parallel()

	env := newNodeServerTestEnv()
	tests := []struct {
		msg           string
		request       *proto.NodeExpandVolumeRequest
		expectedError error
	}{
		{
			msg: "EmptyRequest",
			request: &proto.NodeExpandVolumeRequest{
				VolumePath: "/path",
			},
			expectedError: fmt.Errorf("VolumeID must be provided"),
		},
		{
			msg: "EmptyRequest",
			request: &proto.NodeExpandVolumeRequest{
				VolumeId: "pvc-1",
			},
			expectedError: fmt.Errorf("VolumePath must be provided"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			_, err := env.service.NodeExpandVolume(t.Context(), testCase.request)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), testCase.expectedError.Error())
		})
	}
}

func TestNodeServiceNodeGetCapabilities(t *testing.T) {
	env := newNodeServerTestEnv()

	resp, err := env.service.NodeGetCapabilities(t.Context(), &proto.NodeGetCapabilitiesRequest{})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.GetCapabilities())

	for _, capability := range resp.GetCapabilities() {
		switch capability.GetRpc().GetType() { //nolint:exhaustive
		case proto.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME:
		case proto.NodeServiceCapability_RPC_EXPAND_VOLUME:
		case proto.NodeServiceCapability_RPC_GET_VOLUME_STATS:
		default:
			t.Fatalf("Unknown capability: %v", capability.GetType())
		}
	}
}

func TestNodeServiceNodeGetInfo(t *testing.T) {
	t.Parallel()

	nodes := &corev1.NodeList{
		Items: []corev1.Node{
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-region",
					Labels: map[string]string{
						corev1.LabelTopologyRegion: "region",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-zone",
					Labels: map[string]string{
						corev1.LabelTopologyZone: "zone",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-max-volumes-override",
					Labels: map[string]string{
						corev1.LabelTopologyRegion:        "region",
						corev1.LabelTopologyZone:          "zone",
						csi.NodeLabelMaxVolumeAttachments: "2",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-max-volumes-override-negative",
					Labels: map[string]string{
						corev1.LabelTopologyRegion:        "region",
						corev1.LabelTopologyZone:          "zone",
						csi.NodeLabelMaxVolumeAttachments: "-1",
					},
				},
			},
			{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Node",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-max-volumes-override-over-limit",
					Labels: map[string]string{
						corev1.LabelTopologyRegion:        "region",
						corev1.LabelTopologyZone:          "zone",
						csi.NodeLabelMaxVolumeAttachments: fmt.Sprintf("%d", csi.VolumesPerNodeHardLimit+1),
					},
				},
			},
		},
	}

	tests := []struct {
		msg              string
		kclient          kubernetes.Interface
		nodeName         string
		expectedError    error
		expectedResponse *proto.NodeGetInfoResponse
	}{
		{
			msg:           "NodeDesntExist",
			kclient:       fake.NewSimpleClientset(nodes),
			nodeName:      "nonexist-node",
			expectedError: fmt.Errorf("failed to get node nonexist-node: nodes \"%s\" not found", "nonexist-node"),
		},
		{
			msg:           "RegionNode",
			kclient:       fake.NewSimpleClientset(nodes),
			nodeName:      "node-zone",
			expectedError: fmt.Errorf("failed to get region for node node-zone"),
		},
		{
			msg:           "ZoneNode",
			kclient:       fake.NewSimpleClientset(nodes),
			nodeName:      "node-region",
			expectedError: fmt.Errorf("failed to get zone for node node-region"),
		},
		{
			msg:      "GoodNode",
			kclient:  fake.NewSimpleClientset(nodes),
			nodeName: "node-1",
			expectedResponse: &proto.NodeGetInfoResponse{
				NodeId:            "node-1",
				MaxVolumesPerNode: csi.DefaultMaxVolumesPerNode,
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
			},
		},
		{
			msg:      "GoodNode",
			kclient:  fake.NewSimpleClientset(nodes),
			nodeName: "node-max-volumes-override",
			expectedResponse: &proto.NodeGetInfoResponse{
				NodeId:            "node-max-volumes-override",
				MaxVolumesPerNode: 2,
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
			},
		},
		{
			msg:      "GoodNode",
			kclient:  fake.NewSimpleClientset(nodes),
			nodeName: "node-max-volumes-override-negative",
			expectedResponse: &proto.NodeGetInfoResponse{
				NodeId:            "node-max-volumes-override-negative",
				MaxVolumesPerNode: csi.DefaultMaxVolumesPerNode,
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
			},
		},
		{
			msg:      "GoodNode",
			kclient:  fake.NewSimpleClientset(nodes),
			nodeName: "node-max-volumes-override-over-limit",
			expectedResponse: &proto.NodeGetInfoResponse{
				NodeId:            "node-max-volumes-override-over-limit",
				MaxVolumesPerNode: csi.DefaultMaxVolumesPerNode,
				AccessibleTopology: &proto.Topology{
					Segments: map[string]string{
						corev1.LabelTopologyRegion: "region",
						corev1.LabelTopologyZone:   "zone",
					},
				},
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			svc := csi.NewNodeService(testCase.nodeName, testCase.kclient)
			assert.NotNil(t, svc)

			res, err := svc.NodeGetInfo(t.Context(), &proto.NodeGetInfoRequest{})

			if testCase.expectedError == nil {
				assert.Nil(t, err)
				assert.NotNil(t, res)
				assert.Equal(t, testCase.expectedResponse, res)
			} else {
				assert.NotNil(t, err)
				assert.Equal(t, err.Error(), testCase.expectedError.Error())
			}
		})
	}
}
