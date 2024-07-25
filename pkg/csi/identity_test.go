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
	"testing"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"

	"github.com/leahcimic/proxmox-csi-plugin/pkg/csi"
)

var _ proto.IdentityServer = (*csi.IdentityService)(nil)

type identityServiceTestEnv struct {
	service *csi.IdentityService
}

func newIdentityServerTestEnv() identityServiceTestEnv {
	return identityServiceTestEnv{
		service: csi.NewIdentityService(),
	}
}

func TestGetPluginInfo(t *testing.T) {
	env := newIdentityServerTestEnv()

	resp, err := env.service.GetPluginInfo(context.Background(), &proto.GetPluginInfoRequest{})
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	assert.Equal(t, resp.GetName(), csi.DriverName)
	assert.Equal(t, resp.GetVendorVersion(), csi.DriverVersion)
}

func TestGetPluginCapabilities(t *testing.T) {
	env := newIdentityServerTestEnv()

	resp, err := env.service.GetPluginCapabilities(context.Background(), &proto.GetPluginCapabilitiesRequest{})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.GetCapabilities())

	for _, capability := range resp.GetCapabilities() {
		if capability.GetVolumeExpansion() != nil {
			switch capability.GetVolumeExpansion().GetType() { //nolint:exhaustive
			case proto.PluginCapability_VolumeExpansion_ONLINE:
			case proto.PluginCapability_VolumeExpansion_OFFLINE:
			default:
				t.Fatalf("Unknown capability: %v", capability.GetVolumeExpansion().GetType())
			}
		}

		if capability.GetService() != nil {
			switch capability.GetService().GetType() { //nolint:exhaustive
			case proto.PluginCapability_Service_CONTROLLER_SERVICE:
			case proto.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS:
			default:
				t.Fatalf("Unknown capability: %v", capability.GetService().GetType())
			}
		}
	}
}

func TestProbe(t *testing.T) {
	env := newIdentityServerTestEnv()

	resp, err := env.service.Probe(context.Background(), &proto.ProbeRequest{})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
}
