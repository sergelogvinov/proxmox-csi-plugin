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
	"net/http"
	"strings"
	"testing"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/jarcoal/httpmock"
	"github.com/leahcimic/proxmox-csi-plugin/pkg/csi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proxmox "github.com/sergelogvinov/proxmox-cloud-controller-manager/pkg/cluster"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/leahcimic/proxmox-csi-plugin/pkg/csi"
)

var _ proto.ControllerServer = (*csi.ControllerService)(nil)

type csiTestSuite struct {
	suite.Suite

	s *csi.ControllerService
}

func (ts *csiTestSuite) SetupTest() {
	cfg, err := proxmox.ReadCloudConfig(strings.NewReader(`
clusters:
- url: https://127.0.0.1:8006/api2/json
  insecure: false
  token_id: "user!token-id"
  token_secret: "secret"
  region: cluster-1
- url: https://127.0.0.2:8006/api2/json
  insecure: false
  token_id: "user!token-id"
  token_secret: "secret"
  region: cluster-2
`))
	if err != nil {
		ts.T().Fatalf("failed to read config: %v", err)
	}

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/cluster/resources",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"node":   "pve-1",
						"type":   "lxc",
						"vmid":   100,
						"name":   "cluster-1-node-1",
						"maxcpu": 4,
						"maxmem": 10 * 1024 * 1024 * 1024,
					},
					map[string]interface{}{
						"node":   "pve-2",
						"type":   "lxc",
						"vmid":   101,
						"name":   "cluster-1-node-2",
						"maxcpu": 2,
						"maxmem": 5 * 1024 * 1024 * 1024,
					},
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.2:8006/api2/json/cluster/resources",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"node":   "pve-3",
						"type":   "lxc",
						"vmid":   100,
						"name":   "cluster-2-node-1",
						"maxcpu": 1,
						"maxmem": 2 * 1024 * 1024 * 1024,
					},
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"node":   "pve-1",
						"status": "online",
					},
					map[string]interface{}{
						"node":   "pve-2",
						"status": "online",
					},
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-1/lxc/100/config",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"vmid":  100,
					"scsi0": "local-lvm:vm-100-disk-0,size=10G",
					"scsi1": "local-lvm:vm-9999-pvc-123,backup=0,iothread=1,wwn=0x5056432d49443031",
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-2/lxc/101/config",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"vmid":  101,
					"scsi0": "local-lvm:vm-101-disk-0,size=10G",
					"scsi1": "local-lvm:vm-101-disk-1,size=1G",
					"scsi3": "local-lvm:vm-101-disk-2,size=1G",
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/cluster-1-node-2/lxc/101/config",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"vmid":  101,
					"scsi0": "local-lvm:vm-101-disk-0,size=10G",
				},
			})
		},
	)

	httpmock.RegisterResponder("PUT", "https://127.0.0.1:8006/api2/json/nodes/pve-1/lxc/100/resize",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/storage/status",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/storage/local-lvm",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"shared": 0,
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/storage/smb",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"shared": 1,
					"type":   "cifs",
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/smb/content",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"format": "raw",
						"size":   1024 * 1024 * 1024,
						"volid":  "smb:vm-9999-pvc-smb",
					},
				},
			})
		},
	)

	httpmock.RegisterResponder("POST", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/smb/content",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": "smb:vm-9999-volume-id",
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/local-lvm/status",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": map[string]interface{}{
					"type":  "lvmthin",
					"total": 100 * 1024 * 1024 * 1024,
					"used":  50 * 1024 * 1024 * 1024,
					"avail": 50 * 1024 * 1024 * 1024,
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/wrong-volume/content",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/local-lvm/content",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"format": "raw",
						"size":   1024 * 1024 * 1024,
						"volid":  "local-lvm:vm-9999-pvc-123",
					},
					map[string]interface{}{
						"format": "raw",
						"size":   5 * 1024 * 1024 * 1024,
						"volid":  "local-lvm:vm-9999-pvc-exist",
					},
					map[string]interface{}{
						"format": "raw",
						"size":   csi.MinVolumeSize * 1024 * 1024 * 1024,
						"volid":  "local-lvm:vm-9999-pvc-exist-same-size",
					},
					map[string]interface{}{
						"format": "raw",
						"size":   1024 * 1024 * 1024,
						"volid":  "local-lvm:vm-9999-pvc-error",
					},
				},
			})
		},
	)

	httpmock.RegisterResponder("POST", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/local-lvm/content",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": "local-lvm:vm-9999-pvc-123",
			})
		},
	)

	httpmock.RegisterResponder("DELETE", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/local-lvm/content/vm-9999-pvc-123",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{})
		},
	)

	httpmock.RegisterResponder("DELETE", "https://127.0.0.1:8006/api2/json/nodes/pve-1/storage/local-lvm/content/vm-9999-pvc-error",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"errors": "fake error delete disk",
			})
		},
	)

	cluster, err := proxmox.NewCluster(&cfg, &http.Client{})
	if err != nil {
		ts.T().Fatalf("failed to create proxmox cluster client: %v", err)
	}

	nodes := &corev1.NodeList{
		Items: []corev1.Node{
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
			},
		},
	}

	kclient := fake.NewSimpleClientset(nodes)

	ts.s = &csi.ControllerService{
		Cluster: cluster,
		Kclient: kclient,
	}
}

func TestSuiteCCM(t *testing.T) {
	suite.Run(t, new(csiTestSuite))
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
func (ts *csiTestSuite) TestCreateVolume() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

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
			msg: "VolumeParameters",
			request: &proto.CreateVolumeRequest{
				Name:                      "volume-id",
				VolumeCapabilities:        []*proto.VolumeCapability{volcap},
				CapacityRange:             volsize,
				AccessibilityRequirements: topology,
			},
			expectedError: status.Error(codes.InvalidArgument, "Parameters must be provided"),
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
			expectedError: status.Error(codes.InvalidArgument, "Parameters storage must be provided"),
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
			expectedError: status.Error(codes.InvalidArgument, "Parameters blockSize must be a number"),
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
			expectedError: status.Error(codes.InvalidArgument, "Parameters inodeSize must be a number"),
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
			expectedError: status.Error(codes.Internal, "cannot find best region and zone: failed to find node with storage fake-storage"),
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
			msg: "UnknowRegion",
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
			expectedError: status.Error(codes.Internal, "proxmox cluster unknown-region not found"),
		},
		{
			msg: "NonSupportZonalSMB",
			request: &proto.CreateVolumeRequest{
				Name: "volume-id",
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
			expectedError: status.Error(codes.Internal, "error: shared storage type nfs,cifs,pbs are not supported"),
		},
		{
			msg: "WrongClusterNotFound",
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
			expectedError: status.Error(codes.Internal, "proxmox cluster fake-region not found"),
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
								corev1.LabelTopologyRegion: "Region-1",
								corev1.LabelTopologyZone:   "pve-1",
							},
						},
					},
				},
			},
			expectedError: status.Error(codes.Internal, "proxmox cluster Region-1 not found"),
		},
		{
			msg: "PVCAlreadyExist",
			request: &proto.CreateVolumeRequest{
				Name:               "pvc-exist",
				Parameters:         volParam,
				VolumeCapabilities: []*proto.VolumeCapability{volcap},
				CapacityRange: &proto.CapacityRange{
					RequiredBytes: 100,
				},
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
			expectedError: status.Error(codes.AlreadyExists, "volume already exists with same name and different capacity"),
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
					VolumeContext: volParam,
					CapacityBytes: int64(1024 * 1024 * 1024),
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
					VolumeContext: volParam,
					CapacityBytes: int64(1024 * 1024 * 1024),
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
		testCase := testCase

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
func (ts *csiTestSuite) TestDeleteVolume() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	tests := []struct {
		msg           string
		request       *proto.DeleteVolumeRequest
		expected      *proto.DeleteVolumeResponse
		expectedError error
	}{
		{
			msg:           "VolumeID",
			request:       &proto.DeleteVolumeRequest{},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be provided"),
		},
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
			expectedError: status.Error(codes.Internal, "proxmox cluster fake-region not found"),
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
			expectedError: status.Error(codes.Internal, "failed to delete volume: vm-9999-pvc-error"),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

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

func (ts *csiTestSuite) TestControllerServiceControllerGetCapabilities() {
	resp, err := ts.s.ControllerGetCapabilities(context.Background(), &proto.ControllerGetCapabilitiesRequest{})
	ts.Require().NoError(err)
	ts.Require().NotNil(resp)

	if len(resp.GetCapabilities()) != 6 {
		ts.T().Fatalf("unexpected number of capabilities: %d", len(resp.GetCapabilities()))
	}
}

//nolint:dupl
func (ts *csiTestSuite) TestControllerPublishVolumeError() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

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
		expected      *proto.ControllerPublishVolumeResponse
		expectedError error
	}{
		{
			msg: "VolumeID",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be provided"),
		},
		{
			msg: "NodeID",
			request: &proto.ControllerPublishVolumeRequest{
				VolumeId:         "volume-id",
				VolumeCapability: volcap,
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
			msg: "VolumeContext",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "volume-id",
				VolumeCapability: volcap,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeContext must be provided"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "volume-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "node-id",
				VolumeId:         "fake-region/node-id/data/volume-id",
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.Internal, "proxmox cluster fake-region not found"),
		},
		// {
		// 	msg: "WrongNode",
		// 	request: &proto.ControllerPublishVolumeRequest{
		// 		NodeId:           "cluster-1-node-2",
		// 		VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
		// 		VolumeCapability: volcap,
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
				VolumeCapability: volcap,
				VolumeContext:    volCtx,
			},
			expectedError: status.Error(codes.NotFound, "volume not found"),
		},
		{
			msg: "VolumeAlreadyAttached",
			request: &proto.ControllerPublishVolumeRequest{
				NodeId:           "cluster-1-node-1",
				VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
				VolumeCapability: volcap,
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
		testCase := testCase

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
func (ts *csiTestSuite) TestControllerUnpublishVolumeError() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

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
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be provided"),
		},
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
			expectedError: status.Error(codes.Internal, "proxmox cluster fake-region not found"),
		},
		{
			msg: "WrongNode",
			request: &proto.ControllerUnpublishVolumeRequest{
				NodeId:   "cluster-1-node-3",
				VolumeId: "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
			},
			expectedError: status.Error(codes.Internal, "vm 'cluster-1-node-3' not found"),
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
		testCase := testCase

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

func (ts *csiTestSuite) TestValidateVolumeCapabilities() {
	_, err := ts.s.ValidateVolumeCapabilities(context.Background(), &proto.ValidateVolumeCapabilitiesRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *csiTestSuite) TestListVolumes() {
	_, err := ts.s.ListVolumes(context.Background(), &proto.ListVolumesRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *csiTestSuite) TestGetCapacity() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

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
			expectedError: status.Error(codes.InvalidArgument, "region, zone and storageName must be provided"),
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
					csi.StorageIDKey: "storage",
				},
			},
			expectedError: status.Error(codes.InvalidArgument, "region, zone and storageName must be provided"),
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
					csi.StorageIDKey: "storage",
				},
			},
			expectedError: status.Error(codes.InvalidArgument, "region, zone and storageName must be provided"),
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
			expectedError: status.Error(codes.InvalidArgument, "region, zone and storageName must be provided"),
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
					csi.StorageIDKey: "storage",
				},
			},
			expectedError: status.Error(codes.Internal, "proxmox cluster region not found"),
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
			expectedError: status.Error(codes.Internal, "storage STATUS not readable"),
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
		testCase := testCase

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

func (ts *csiTestSuite) TestCreateSnapshot() {
	_, err := ts.s.CreateSnapshot(context.Background(), &proto.CreateSnapshotRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *csiTestSuite) TestDeleteSnapshot() {
	_, err := ts.s.DeleteSnapshot(context.Background(), &proto.DeleteSnapshotRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *csiTestSuite) TestListSnapshots() {
	_, err := ts.s.ListSnapshots(context.Background(), &proto.ListSnapshotsRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}

func (ts *csiTestSuite) TestControllerExpandVolumeError() {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	capRange := &proto.CapacityRange{
		RequiredBytes: 100,
		LimitBytes:    150,
	}

	volCapability := &proto.VolumeCapability{
		AccessMode: &proto.VolumeCapability_AccessMode{
			Mode: proto.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
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
				CapacityRange:    capRange,
				VolumeCapability: volCapability,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be provided"),
		},
		{
			msg: "CapacityRange",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:         "volume-id",
				VolumeCapability: volCapability,
			},
			expectedError: status.Error(codes.InvalidArgument, "CapacityRange must be provided"),
		},
		{
			msg: "CapacityRangeLimit",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId: "volume-id",
				CapacityRange: &proto.CapacityRange{
					RequiredBytes: 150,
					LimitBytes:    100,
				},
				VolumeCapability: volCapability,
			},
			expectedError: status.Error(codes.OutOfRange, "after round-up, volume size exceeds the limit specified"),
		},
		{
			msg: "WrongVolumeID",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:         "volume-id",
				CapacityRange:    capRange,
				VolumeCapability: volCapability,
			},
			expectedError: status.Error(codes.InvalidArgument, "VolumeID must be in the format of region/zone/storageName/diskName"),
		},
		{
			msg: "WrongCluster",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:         "fake-region/node/data/volume-id",
				CapacityRange:    capRange,
				VolumeCapability: volCapability,
			},
			expectedError: status.Error(codes.Internal, "proxmox cluster fake-region not found"),
		},
		{
			msg: "WrongPVC",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-none",
				CapacityRange:    capRange,
				VolumeCapability: volCapability,
			},
			expected: &proto.ControllerExpandVolumeResponse{},
		},
		{
			msg: "UpublishedVolume",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-error",
				CapacityRange:    capRange,
				VolumeCapability: volCapability,
			},
			expectedError: status.Error(codes.Internal, "cannot resize unpublished volumeID"),
		},
		{
			msg: "ExpandVolume",
			request: &proto.ControllerExpandVolumeRequest{
				VolumeId:         "cluster-1/pve-1/local-lvm/vm-9999-pvc-123",
				CapacityRange:    capRange,
				VolumeCapability: volCapability,
			},
			expected: &proto.ControllerExpandVolumeResponse{
				CapacityBytes:         100,
				NodeExpansionRequired: true,
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase

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

func (ts *csiTestSuite) TestControllerGetVolume() {
	_, err := ts.s.ControllerGetVolume(context.Background(), &proto.ControllerGetVolumeRequest{})
	ts.Require().Error(err)
	ts.Require().Equal(status.Error(codes.Unimplemented, ""), err)
}
