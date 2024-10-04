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
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proxmox "github.com/sergelogvinov/proxmox-cloud-controller-manager/pkg/cluster"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/metrics"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"

	corev1 "k8s.io/api/core/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	vmID = 9999

	deviceNamePrefix = "scsi"
)

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_VOLUME,
	csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
}

// ControllerService is the controller service for the CSI driver
type ControllerService struct {
	Cluster *proxmox.Cluster
	Kclient clientkubernetes.Interface

	volumeLocks sync.Mutex

	csi.UnimplementedControllerServer
}

// NewControllerService returns a new controller service
func NewControllerService(kclient *clientkubernetes.Clientset, cloudConfig string) (*ControllerService, error) {
	cfg, err := proxmox.ReadCloudConfigFromFile(cloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	cluster, err := proxmox.NewCluster(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxmox cluster client: %v", err)
	}

	return &ControllerService{
		Cluster: cluster,
		Kclient: kclient,
	}, nil
}

// CreateVolume creates a volume
//
//nolint:gocyclo,cyclop
func (d *ControllerService) CreateVolume(_ context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).InfoS("CreateVolume: called", "args", protosanitizer.StripSecrets(request))

	pvc := request.GetName()
	if len(pvc) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeName must be provided")
	}

	volCapabilities := request.GetVolumeCapabilities()
	if volCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapabilities must be provided")
	}

	params := request.GetParameters()
	if params == nil {
		return nil, status.Error(codes.InvalidArgument, "Parameters must be provided")
	}

	if params[StorageIDKey] == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Parameters %s must be provided", StorageIDKey)
	}

	if params[StorageBlockSizeKey] != "" {
		if _, err := strconv.Atoi(params[StorageBlockSizeKey]); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Parameters %s must be a number", StorageBlockSizeKey)
		}
	}

	if params[StorageInodeSizeKey] != "" {
		if _, err := strconv.Atoi(params[StorageInodeSizeKey]); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Parameters %s must be a number", StorageInodeSizeKey)
		}
	}

	// Volume Size - Default is 10 GiB
	volSizeBytes := int64(DefaultVolumeSize * 1024 * 1024 * 1024)
	if request.GetCapacityRange() != nil {
		volSizeBytes = request.GetCapacityRange().GetRequiredBytes()
	}

	accessibleTopology := request.GetAccessibilityRequirements()

	region, zone := locationFromTopologyRequirement(accessibleTopology)
	if region == "" {
		err := status.Error(codes.Internal, "cannot find best region")
		klog.ErrorS(err, "CreateVolume: region is empty", "accessibleTopology", accessibleTopology)

		return nil, err
	}

	cl, err := d.Cluster.GetProxmoxCluster(region)
	if err != nil {
		klog.ErrorS(err, "CreateVolume: failed to get proxmox cluster", "cluster", region)

		return nil, status.Error(codes.Internal, err.Error())
	}

	if zone == "" {
		if zone, err = getNodeWithStorage(cl, params[StorageIDKey]); err != nil {
			klog.ErrorS(err, "CreateVolume: failed to get node with storage", "cluster", region, "storage", params[StorageIDKey])

			return nil, status.Errorf(codes.Internal, "cannot find best region and zone: %v", err)
		}
	}

	if region == "" || zone == "" {
		klog.ErrorS(err, "CreateVolume: region or zone is empty", "cluster", region, "accessibleTopology", accessibleTopology)

		return nil, status.Error(codes.Internal, "cannot find best region and zone")
	}

	storageConfig, err := cl.GetStorageConfig(params[StorageIDKey])
	if err != nil {
		klog.ErrorS(err, "CreateVolume: failed to get proxmox storage config", "cluster", region, "storageID", params[StorageIDKey])

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(4).InfoS("CreateVolume", "storageConfig", storageConfig)

	topology := &csi.Topology{
		Segments: map[string]string{
			corev1.LabelTopologyRegion: region,
			corev1.LabelTopologyZone:   zone,
		},
	}

	if storageConfig["shared"] != nil && int(storageConfig["shared"].(float64)) == 1 {
		// https://pve.proxmox.com/wiki/Storage only block/local storage are supported
		switch storageConfig["type"].(string) {
		case "cifs", "pbs":
			return nil, status.Error(codes.Internal, "error: shared storage type cifs, pbs are not supported")
		}

		topology = &csi.Topology{
			Segments: map[string]string{
				corev1.LabelTopologyRegion: region,
			},
		}
	}

	vol := volume.NewVolume(region, zone, params[StorageIDKey], fmt.Sprintf("vm-%d-%s", vmID, pvc))
	if storageConfig["path"] != nil && storageConfig["path"].(string) != "" {
		vol = volume.NewVolume(region, zone, params[StorageIDKey], fmt.Sprintf("%d/vm-%d-%s.raw", vmID, vmID, pvc))
	}

	// Check if volume already exists, and use it if it has the same size, otherwise create a new one
	size, err := getVolumeSize(cl, vol)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "CreateVolume: failed to check if pvc exist", "cluster", region, "volumeID", vol.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		mc := metrics.NewMetricContext("createVolume")

		err = createVolume(cl, vol, volSizeBytes)
		if mc.ObserveRequest(err) != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else if size != volSizeBytes {
		klog.ErrorS(err, "CreateVolume: volume is already exists", "cluster", region, "volumeID", vol.VolumeID(), "size", size)

		return nil, status.Error(codes.AlreadyExists, "volume already exists with same name and different capacity")
	}

	volID := vol.VolumeID()
	if storageConfig["shared"] != nil && int(storageConfig["shared"].(float64)) == 1 {
		volID = vol.VolumeSharedID()
	}

	klog.V(3).InfoS("CreateVolume: volume created", "cluster", vol.Cluster(), "volumeID", volID, "size", volSizeBytes)

	volume := csi.Volume{
		VolumeId:      volID,
		VolumeContext: params,
		ContentSource: request.GetVolumeContentSource(),
		CapacityBytes: volSizeBytes,
		AccessibleTopology: []*csi.Topology{
			topology,
		},
	}

	return &csi.CreateVolumeResponse{Volume: &volume}, nil
}

// DeleteVolume deletes a volume.
func (d *ControllerService) DeleteVolume(_ context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).InfoS("DeleteVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	vol, err := volume.NewVolumeFromVolumeID(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "DeleteVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	exist, err := isPvcExists(cl, vol)
	if err != nil {
		klog.ErrorS(err, "DeleteVolume: failed to verify the existence of the PVC", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.Internal, err.Error())
	}

	if !exist {
		klog.V(3).InfoS("DeleteVolume: is already deleted", "volumeID", vol.VolumeID())

		return &csi.DeleteVolumeResponse{}, nil
	}

	vmr, err := getVMRefByVolume(cl, vol)
	if err != nil {
		klog.ErrorS(err, "DeleteVolume: failed to get vm ref by volume", "cluster", vol.Cluster(), "volumeName", vol.Disk())

		return nil, status.Error(codes.Internal, err.Error())
	}

	mc := metrics.NewMetricContext("deleteVolume")
	if _, err := cl.DeleteVolume(vmr, vol.Storage(), vol.Disk()); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "DeleteVolume: failed to delete volume", "cluster", vol.Cluster(), "volumeName", vol.Disk())

		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete volume: %s", vol.Disk()))
	}

	klog.V(3).InfoS("DeleteVolume: volume deleted", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerGetCapabilities get controller capabilities.
func (d *ControllerService) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).InfoS("ControllerGetCapabilities: called")

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
func (d *ControllerService) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.V(4).InfoS("ControllerPublishVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	nodeID := request.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeID must be provided")
	}

	if request.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	volCtx := request.GetVolumeContext()
	if volCtx == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeContext must be provided")
	}

	vol, err := volume.NewVolumeFromVolumeID(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	var vmr *pxapi.VmRef

	vmrid, zone, err := tools.ProxmoxVMID(ctx, d.Kclient, nodeID)
	if err != nil {
		klog.InfoS("ControllerPublishVolume: failed to get proxmox vmrID from ProviderID", "cluster", vol.Cluster(), "nodeID", nodeID)

		vmr, err = cl.GetVmRefByName(nodeID)
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: failed to get vm ref by nodeID", "cluster", vol.Cluster(), "nodeID", nodeID)

			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		vmr = pxapi.NewVmRef(vmrid)
		vmr.SetNode(zone)
		vmr.SetVmType("qemu")
	}

	if vol.Zone() == "" {
		vol = volume.NewVolume(vol.Region(), vmr.Node(), vol.Storage(), vol.Disk())
	}

	options := map[string]string{
		"backup":   "0",
		"iothread": "1",
	}

	// Temporary workaround for unsafe mount, better to use a VolumeAttributesClass resource
	unsafeEnv := os.Getenv("UNSAFEMOUNT")
	if unsafeEnv == "true" { // nolint: goconst
		options = map[string]string{
			"iothread": "1",
		}
	}

	if request.GetReadonly() {
		options["ro"] = "1"
	}

	if volCtx[StorageSSDKey] == "true" {
		options["ssd"] = "1"
		options["discard"] = "on"
	}

	if volCtx[StorageCacheKey] != "" {
		options["cache"] = volCtx[StorageCacheKey]
	}

	if volCtx[StorageDiskIOPSKey] != "" {
		iops, err := strconv.Atoi(volCtx[StorageDiskIOPSKey]) //nolint:govet
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: must be a number", StorageDiskIOPSKey, volCtx[StorageDiskIOPSKey])

			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed %s must be a number: %v", StorageDiskIOPSKey, err))
		}

		options["iops"] = strconv.Itoa(iops)
	}

	if volCtx[StorageDiskMBpsKey] != "" {
		mbps, err := strconv.Atoi(volCtx[StorageDiskMBpsKey]) //nolint:govet
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: must be a number", StorageDiskMBpsKey, volCtx[StorageDiskMBpsKey])

			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed %s must be a number: %v", StorageDiskMBpsKey, err))
		}

		options["mbps"] = strconv.Itoa(mbps)
	}

	exist, err := isPvcExists(cl, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to verify the existence of the PVC", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.Internal, err.Error())
	}

	if !exist {
		return nil, status.Error(codes.NotFound, "volume not found")
	}

	d.volumeLocks.Lock()
	defer d.volumeLocks.Unlock()

	mc := metrics.NewMetricContext("attachVolume")

	pvInfo, err := attachVolume(cl, vmr, vol.Storage(), vol.Disk(), options)
	if mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to attach volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(3).InfoS("ControllerPublishVolume: volume published", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

	return &csi.ControllerPublishVolumeResponse{PublishContext: pvInfo}, nil
}

// ControllerUnpublishVolume unpublish a volume
func (d *ControllerService) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).InfoS("ControllerUnpublishVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	nodeID := request.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeID must be provided")
	}

	vol, err := volume.NewVolumeFromVolumeID(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerUnpublishVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	var vmr *pxapi.VmRef

	vmrid, zone, err := tools.ProxmoxVMID(ctx, d.Kclient, nodeID)
	if err != nil {
		klog.InfoS("ControllerUnpublishVolume: failed to get proxmox vmrID from ProviderID", "cluster", vol.Cluster(), "nodeID", nodeID)

		vmr, err = cl.GetVmRefByName(nodeID)
		if err != nil {
			klog.ErrorS(err, "ControllerUnpublishVolume: failed to get vm ref by nodeID", "cluster", vol.Cluster(), "nodeID", nodeID)

			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		vmr = pxapi.NewVmRef(vmrid)
		vmr.SetNode(zone)
		vmr.SetVmType("qemu")
	}

	mc := metrics.NewMetricContext("detachVolume")

	if err := detachVolume(cl, vmr, vol.Disk()); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerUnpublishVolume: failed to detach volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(3).InfoS("ControllerUnpublishVolume: volume unpublished", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities validate volume capabilities
func (d *ControllerService) ValidateVolumeCapabilities(_ context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(4).InfoS("ValidateVolumeCapabilities: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ListVolumes list volumes
func (d *ControllerService) ListVolumes(_ context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(4).InfoS("ListVolumes: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// GetCapacity get capacity
func (d *ControllerService) GetCapacity(_ context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	klog.V(5).InfoS("GetCapacity: called", "args", protosanitizer.StripSecrets(request))

	topology := request.GetAccessibleTopology()
	if topology != nil {
		region := topology.GetSegments()[corev1.LabelTopologyRegion]
		zone := topology.GetSegments()[corev1.LabelTopologyZone]
		storageID := request.GetParameters()[StorageIDKey]

		if region == "" || zone == "" || storageID == "" {
			return nil, status.Error(codes.InvalidArgument, "region, zone and storageName must be provided")
		}

		klog.V(3).InfoS("GetCapacity", "region", region, "zone", zone, "storageID", storageID)

		cl, err := d.Cluster.GetProxmoxCluster(region)
		if err != nil {
			klog.ErrorS(err, "GetCapacity: failed to get proxmox cluster", "cluster", region)

			return nil, status.Error(codes.Internal, err.Error())
		}

		vmr := pxapi.NewVmRef(vmID)
		vmr.SetNode(zone)
		vmr.SetVmType("qemu")

		availableCapacity := int64(0)

		mc := metrics.NewMetricContext("storageStatus")

		storage, err := cl.GetStorageStatus(vmr, storageID)
		if mc.ObserveRequest(err) != nil {
			klog.ErrorS(err, "GetCapacity: failed to get storage status", "cluster", region, "storageID", storageID)

			if !strings.Contains(err.Error(), "Parameter verification failed") {
				return nil, status.Error(codes.Internal, err.Error())
			}
		} else {
			availableCapacity = int64(storage["avail"].(float64))
		}

		return &csi.GetCapacityResponse{
			// MinimumVolumeSize: MinVolumeSize * 1024 * 1024 * 1024,
			AvailableCapacity: availableCapacity,
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "no topology specified")
}

// CreateSnapshot create a snapshot
func (d *ControllerService) CreateSnapshot(_ context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(4).InfoS("CreateSnapshot: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// DeleteSnapshot delete a snapshot
func (d *ControllerService) DeleteSnapshot(_ context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).InfoS("DeleteSnapshot: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ListSnapshots list snapshots
func (d *ControllerService) ListSnapshots(_ context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).InfoS("ListSnapshots: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerExpandVolume expand a volume
func (d *ControllerService) ControllerExpandVolume(_ context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).InfoS("ControllerExpandVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	volCapability := request.GetVolumeCapability()
	if volCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	capacityRange := request.GetCapacityRange()
	if capacityRange == nil {
		return nil, status.Error(codes.InvalidArgument, "CapacityRange must be provided")
	}

	volSizeBytes := request.GetCapacityRange().GetRequiredBytes()
	maxVolSize := capacityRange.GetLimitBytes()

	if maxVolSize > 0 && maxVolSize < volSizeBytes {
		return nil, status.Error(codes.OutOfRange, "after round-up, volume size exceeds the limit specified")
	}

	vol, err := volume.NewVolumeFromVolumeID(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerExpandVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	exist, err := isPvcExists(cl, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerExpandVolume: failed to verify the existence of the PVC", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.NotFound, err.Error())
	}

	if !exist {
		klog.V(3).InfoS("ControllerExpandVolume: volume not found", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return &csi.ControllerExpandVolumeResponse{}, nil
	}

	vmlist, err := cl.GetVmList()
	if err != nil {
		klog.ErrorS(err, "ControllerExpandVolume: failed to get vm list", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	vms, ok := vmlist["data"].([]interface{})
	if !ok {
		err = fmt.Errorf("failed to cast response to list, vmlist: %v", vmlist)

		return nil, status.Error(codes.Internal, err.Error())
	}

	for vmii := range vms {
		vm, ok := vms[vmii].(map[string]interface{})
		if !ok {
			return nil, status.Errorf(codes.Internal, "failed to cast response to map, vm: %v", vm)
		}

		if vm["type"].(string) != "qemu" {
			klog.V(5).InfoS("ControllerExpandVolume: skipping non-qemu VM", "VM", vm["name"].(string))

			continue
		}

		if vm["node"].(string) == vol.Node() || vol.Node() == "" {
			vmID := int(vm["vmid"].(float64))

			vmr := pxapi.NewVmRef(vmID)
			vmr.SetNode(vol.Node())
			vmr.SetVmType("qemu")

			if vmr.Node() == "" {
				vmr.SetNode(vm["node"].(string))
			}

			config, err := cl.GetVmConfig(vmr)
			if err != nil {
				klog.ErrorS(err, "ControllerExpandVolume: failed to get vm config", "cluster", vol.Cluster(), "vmID", vmr.VmId())

				return nil, status.Error(codes.Internal, err.Error())
			}

			lun, exist := isVolumeAttached(config, vol.Disk())
			if !exist {
				continue
			}

			device := deviceNamePrefix + strconv.Itoa(lun)

			mc := metrics.NewMetricContext("expandVolume")

			if _, err := cl.ResizeQemuDiskRaw(vmr, device, fmt.Sprintf("%d", volSizeBytes)); mc.ObserveRequest(err) != nil {
				klog.ErrorS(err, "ControllerExpandVolume: failed to resize vm disk", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

				return nil, status.Error(codes.Internal, err.Error())
			}

			klog.V(3).InfoS("ControllerExpandVolume: volume expanded", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId(), "size", volSizeBytes)

			return &csi.ControllerExpandVolumeResponse{
				CapacityBytes:         volSizeBytes,
				NodeExpansionRequired: true,
			}, nil
		}
	}

	return nil, status.Error(codes.Internal, "cannot resize unpublished volumeID")
}

// ControllerGetVolume get a volume
func (d *ControllerService) ControllerGetVolume(_ context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.V(4).InfoS("ControllerGetVolume: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerModifyVolume modify a volume
func (d *ControllerService) ControllerModifyVolume(_ context.Context, request *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	klog.V(4).InfoS("ControllerModifyVolume: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}
