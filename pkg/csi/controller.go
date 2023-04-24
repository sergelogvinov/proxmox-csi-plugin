/*
Copyright 2023 sergelogvinov.

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
	"fmt"
	"strings"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proxmox "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmox"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/cloud-provider-openstack/pkg/util"
	"k8s.io/klog"
)

const (
	vmID = 9999
)

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_VOLUME,
}

type controllerService struct {
	cluster proxmox.Client
}

// NewControllerService returns a new controller service
func NewControllerService(cloudConfig string) (*controllerService, error) {
	cfg, err := proxmox.ReadFromFileCloudConfig(cloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	cluster, err := proxmox.NewClient(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxmox client: %v", err)
	}

	return &controllerService{
		cluster: *cluster,
	}, nil
}

// CreateVolume creates a volume
//
//lint:gocyclo
func (d *controllerService) CreateVolume(_ context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).Infof("CreateVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	volName := request.GetName()
	if len(volName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume Name cannot be empty")
	}

	volCapabilities := request.GetVolumeCapabilities()
	if volCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities cannot be empty")
	}

	// Volume Size - Default is 10 GiB
	volSizeBytes := int64(10 * 1024 * 1024 * 1024)
	if request.GetCapacityRange() != nil {
		volSizeBytes = request.GetCapacityRange().GetRequiredBytes()
	}

	volSizeGB := int(util.RoundUpSize(volSizeBytes, 1024*1024*1024))

	volCtx := make(map[string]string)
	for k, v := range request.GetParameters() {
		volCtx[k] = v
	}

	volCtx["subPath"] = volName

	accessibleTopology := request.GetAccessibilityRequirements().GetPreferred()

	if len(accessibleTopology) != 0 {
		var (
			region string
			zone   string
			node   string
		)

		for _, t := range accessibleTopology {
			if t.GetSegments()[corev1.LabelTopologyRegion] != "" {
				region = t.GetSegments()[corev1.LabelTopologyRegion]
			}

			if t.GetSegments()[corev1.LabelTopologyZone] != "" {
				zone = t.GetSegments()[corev1.LabelTopologyZone]
			}

			if t.GetSegments()[corev1.LabelHostname] != "" {
				node = t.GetSegments()[corev1.LabelHostname]
			}

			if region != "" && (zone != "" || node != "") {
				volCtx["region"] = region

				if zone != "" {
					volCtx["zone"] = zone
				}

				if node != "" {
					volCtx["node"] = node
				}

				break
			}
		}
	}

	klog.V(4).Infof("CreateVolume: volCtx=%+v volSizeGB=%d", volCtx, volSizeGB)

	volume := csi.Volume{
		VolumeId:           fmt.Sprintf("%s/%s/vm-%d-%s", volCtx["region"], volCtx["zone"], vmID, volName),
		VolumeContext:      volCtx,
		ContentSource:      request.GetVolumeContentSource(),
		CapacityBytes:      int64(volSizeGB * 1024 * 1024 * 1024),
		AccessibleTopology: accessibleTopology,
	}

	cl, err := d.cluster.GetProxmoxCluster(volCtx["region"])
	if err != nil {
		klog.Errorf("failed to get proxmox cluster: %v", err)

		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	diskParams := map[string]interface{}{
		"vmid":     vmID,
		"filename": fmt.Sprintf("vm-%d-%s", vmID, volName),
		"size":     fmt.Sprintf("%dG", volSizeGB),
	}

	klog.V(4).Infof("CreateVolume: diskParams=%+v", diskParams)

	err = cl.CreateVMDisk(volCtx["zone"], volCtx[StorageIDKey], fmt.Sprintf("%s:%s", volCtx[StorageIDKey], diskParams["filename"]), diskParams)
	if err != nil {
		klog.Errorf("failed to create vm disk: %v", err)

		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &csi.CreateVolumeResponse{Volume: &volume}, nil
}

// DeleteVolume deletes a volume.
func (d *controllerService) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).Infof("DeleteVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	volID := request.GetVolumeId()
	if len(volID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "DeleteVolume Volume ID must be provided")
	}

	volIDParts := strings.Split(volID, "/")
	if len(volIDParts) != 3 {
		return nil, status.Error(codes.InvalidArgument, "DeleteVolume Volume ID must be in the format of region/zone/volume-name")
	}

	// cl, err := d.cluster.GetProxmoxCluster(volIDParts[0])
	// if err != nil {
	// 	klog.Errorf("failed to get proxmox cluster: %v", err)

	// 	return nil, status.Error(codes.Internal, err.Error())
	// }

	// vmr := pxapi.NewVmRef(vmID)
	// vmr.SetNode(volIDParts[1])
	// vmr.SetVmType("qemu")

	// result, err := cl.DeleteVolume(vmr, "data", volIDParts[2])
	// if err != nil {
	// 	klog.Errorf("failed to delete volume: %v", err)

	// 	return nil, status.Error(codes.Internal, err.Error())
	// }

	// klog.V(4).Infof("DeleteVolume: Successfully deleted volume %s, result %+v", volID, result)

	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerGetCapabilities get controller capabilities.
func (d *controllerService) ControllerGetCapabilities(ctx context.Context, request *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).Infof("ControllerGetCapabilities: called with args %+v", protosanitizer.StripSecrets(*request))

	caps := []*csi.ControllerServiceCapability{}

	for _, cap := range controllerCaps {
		c := &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		}
		caps = append(caps, c)
	}

	return &csi.ControllerGetCapabilitiesResponse{Capabilities: caps}, nil
}

// ControllerPublishVolume publish a volume
func (d *controllerService) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerPublishVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	if request.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "missing volume id")
	}

	if request.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "missing node id")
	}

	if request.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "missing volume capabilities")
	}

	if request.Readonly {
		return nil, status.Error(codes.InvalidArgument, "readonly volumes are not supported")
	}

	// Publish Volume Info
	pvInfo := map[string]string{}
	pvInfo["DevicePath"] = request.VolumeContext["subPath"]

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: pvInfo,
	}, nil
}

// ControllerUnpublishVolume unpublish a volume
func (d *controllerService) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerUnpublishVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities validate volume capabilities
func (d *controllerService) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(4).Infof("ValidateVolumeCapabilities: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ListVolumes list volumes
func (d *controllerService) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(4).Infof("ListVolumes: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// GetCapacity get capacity
func (d *controllerService) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	klog.V(4).Infof("GetCapacity: called with args %+v", protosanitizer.StripSecrets(*request))

	topology := request.GetAccessibleTopology()
	if topology != nil {
		region := topology.Segments[corev1.LabelTopologyRegion]
		zone := topology.Segments[corev1.LabelTopologyZone]
		storageName := request.GetParameters()[StorageIDKey]

		if region == "" || zone == "" || storageName == "" {
			return nil, status.Error(codes.InvalidArgument, "GetCapacity Region, Zone and StorageName must be provided")
		}

		klog.V(4).Infof("GetCapacity: region=%s, zone=%s, storageName=%s", region, zone, storageName)

		cl, err := d.cluster.GetProxmoxCluster(region)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		vmr := pxapi.NewVmRef(0)
		vmr.SetNode(zone)
		vmr.SetVmType("qemu")

		availableCapacity := int64(0)

		storage, err := cl.GetStorageStatus(vmr, storageName)
		if err != nil {
			klog.Errorf("GetCapacity: failed to get storage status: %v", err)

			if !strings.Contains(err.Error(), "Parameter verification failed") {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		} else {
			availableCapacity = int64(storage["avail"].(float64))
		}

		return &csi.GetCapacityResponse{
			// MinimumVolumeSize: 1024 * 1024 * 1024,
			AvailableCapacity: availableCapacity,
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "GetCapacity: no topology specified")
}

// CreateSnapshot create a snapshot
func (d *controllerService) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(4).Infof("CreateSnapshot: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// DeleteSnapshot delete a snapshot
func (d *controllerService) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).Infof("DeleteSnapshot: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ListSnapshots list snapshots
func (d *controllerService) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).Infof("ListSnapshots: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerExpandVolume expand a volume
func (d *controllerService) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).Infof("ControllerExpandVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerGetVolume get a volume
func (d *controllerService) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.V(4).Infof("ControllerGetVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}
