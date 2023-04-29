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
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes/wrappers"

	"k8s.io/klog/v2"
)

// IdentityService is the identity service for the CSI driver
type IdentityService struct{}

// NewIdentityService returns a new identity service
func NewIdentityService() *IdentityService {
	return &IdentityService{}
}

// GetPluginInfo returns the name and version of the plugin
func (d *IdentityService) GetPluginInfo(context.Context, *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	klog.V(5).Infof("GetPluginInfo: called")

	return &csi.GetPluginInfoResponse{
		Name:          DriverName,
		VendorVersion: DriverVersion,
	}, nil
}

// GetPluginCapabilities returns the capabilities of the plugin
func (d *IdentityService) GetPluginCapabilities(context.Context, *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	klog.V(5).Infof("GetPluginCapabilities: called")

	resp := &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
					},
				},
			},
			{
				Type: &csi.PluginCapability_VolumeExpansion_{
					VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
						Type: csi.PluginCapability_VolumeExpansion_ONLINE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_VolumeExpansion_{
					VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
						Type: csi.PluginCapability_VolumeExpansion_OFFLINE,
					},
				},
			},
		},
	}

	return resp, nil
}

// Probe returns the health and readiness of the plugin
func (d *IdentityService) Probe(_ context.Context, _ *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	klog.V(5).Infof("Probe: called")

	return &csi.ProbeResponse{
		Ready: &wrappers.BoolValue{Value: true},
	}, nil
}
