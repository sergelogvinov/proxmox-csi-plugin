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
			expectedRegion: "",
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
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			region, zone := locationFromTopologyRequirement(testCase.topology)

			assert.Equal(t, testCase.expectedRegion, region)
			assert.Equal(t, testCase.expectedZone, zone)
		})
	}
}
