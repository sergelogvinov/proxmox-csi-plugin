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
	"fmt"
	"testing"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
)

func TestParseEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg            string
		endpoint       string
		expectedScheme string
		expectedAddr   string
		expectedError  error
	}{
		{
			msg:            "unix socket",
			endpoint:       "unix://tmp/csi.sock",
			expectedScheme: "unix",
			expectedAddr:   "/tmp/csi.sock",
		},
		{
			msg:           "http",
			endpoint:      "http://tmp/csi.sock",
			expectedError: fmt.Errorf("unsupported protocol: http"),
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			scheme, addr, err := ParseEndpoint(testCase.endpoint)
			if testCase.expectedError != nil {
				assert.NotNil(t, err)
				assert.Equal(t, err.Error(), testCase.expectedError.Error())
			} else {
				assert.Nil(t, err)
				assert.NotNil(t, scheme, testCase.expectedScheme)
				assert.Equal(t, addr, testCase.expectedAddr)
			}
		})
	}
}

func TestLocationFromTopologyRequirement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg            string
		topology       *proto.TopologyRequirement
		expectedRegion string
		expectedZone   string
	}{
		{
			msg:            "EmptyTopologyRequirement",
			topology:       &proto.TopologyRequirement{},
			expectedRegion: "",
			expectedZone:   "",
		},
		{
			msg: "EmptyTopologyPreferredZone",
			topology: &proto.TopologyRequirement{
				Preferred: []*proto.Topology{
					{
						Segments: map[string]string{
							corev1.LabelTopologyRegion: "region1",
						},
					},
				},
			},
			expectedRegion: "region1",
			expectedZone:   "",
		},
		{
			msg: "EmptyTopologyRequisiteZone",
			topology: &proto.TopologyRequirement{
				Requisite: []*proto.Topology{
					{
						Segments: map[string]string{
							corev1.LabelTopologyRegion: "region1",
						},
					},
				},
			},
			expectedRegion: "region1",
			expectedZone:   "",
		},
		{
			msg: "EmptyTopologyPreferredRegion",
			topology: &proto.TopologyRequirement{
				Preferred: []*proto.Topology{
					{
						Segments: map[string]string{
							corev1.LabelTopologyZone: "zone1",
						},
					},
				},
			},
			expectedRegion: "",
			expectedZone:   "",
		},
		{
			msg: "TopologyPreferred",
			topology: &proto.TopologyRequirement{
				Preferred: []*proto.Topology{
					{
						Segments: map[string]string{
							corev1.LabelTopologyRegion: "region1",
							corev1.LabelTopologyZone:   "zone1",
						},
					},
				},
			},
			expectedRegion: "region1",
			expectedZone:   "zone1",
		},
		{
			msg: "TopologyRequisite",
			topology: &proto.TopologyRequirement{
				Requisite: []*proto.Topology{
					{
						Segments: map[string]string{
							corev1.LabelTopologyRegion: "region1",
							corev1.LabelTopologyZone:   "zone1",
						},
					},
				},
			},
			expectedRegion: "region1",
			expectedZone:   "zone1",
		},
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			region, zone := locationFromTopologyRequirement(testCase.topology)

			assert.Equal(t, testCase.expectedRegion, region)
			assert.Equal(t, testCase.expectedZone, zone)
		})
	}
}

func TestRoundUpSizeBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg                 string
		volumeSize          int64
		allocationUnitBytes int64
		expected            int64
	}{
		{
			msg:                 "Zero size",
			volumeSize:          0,
			allocationUnitBytes: GiB,
			expected:            1024 * 1024 * 1024,
		},
		{
			msg:                 "KiB",
			volumeSize:          123,
			allocationUnitBytes: KiB,
			expected:            1024,
		},
		{
			msg:                 "MiB",
			volumeSize:          123,
			allocationUnitBytes: MiB,
			expected:            1024 * 1024,
		},
		{
			msg:                 "GiB",
			volumeSize:          123,
			allocationUnitBytes: GiB,
			expected:            1024 * 1024 * 1024,
		},
		{
			msg:                 "256MiB -> GiB",
			volumeSize:          256 * 1024 * 1024,
			allocationUnitBytes: GiB,
			expected:            1024 * 1024 * 1024,
		},
		{
			msg:                 "256MiB -> GiB/2",
			volumeSize:          256 * 1024 * 1024,
			allocationUnitBytes: 512 * MiB,
			expected:            512 * 1024 * 1024,
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			expected := RoundUpSizeBytes(testCase.volumeSize, testCase.allocationUnitBytes)
			assert.Equal(t, testCase.expected, expected)
		})
	}
}
