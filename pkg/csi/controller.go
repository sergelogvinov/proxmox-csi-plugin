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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	goproxmox "github.com/sergelogvinov/go-proxmox"
	csiconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/helpers/ptr"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/metrics"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	vmID = 9999

	deviceNamePrefix = "scsi"

	// resizeSizeBytes is the key for the volume context parameter to specify the new size in bytes after restore from snapshot
	// we cannot change size offline, so we pass it via volume context
	resizeSizeBytes = "resizeSizeBytes"
	resizeRequired  = "resizeRequired"
)

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
	csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
	csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	csi.ControllerServiceCapability_RPC_GET_VOLUME,
	csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	csi.ControllerServiceCapability_RPC_MODIFY_VOLUME,
}

// ControllerService is the controller service for the CSI driver
type ControllerService struct {
	csi.UnimplementedControllerServer

	pxpool   *pxpool.ProxmoxPool
	kclient  kubernetes.Interface
	Provider csiconfig.Provider

	vmLocks *VMLocks
}

// NewControllerService returns a new controller service
func NewControllerService(kclient kubernetes.Interface, cloudConfig string) (*ControllerService, error) {
	cfg, err := csiconfig.ReadCloudConfigFromFile(cloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	px, err := pxpool.NewProxmoxPool(cfg.Clusters)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxmox cluster client: %v", err)
	}

	d := &ControllerService{
		pxpool:   px,
		kclient:  kclient,
		Provider: cfg.Features.Provider,
	}

	d.Init()

	return d, nil
}

// Init initializes the controller service
func (d *ControllerService) Init() {
	if d.vmLocks == nil {
		d.vmLocks = NewVMLocks()
	}
}

// CreateVolume creates a volume
//
//nolint:gocyclo,cyclop
func (d *ControllerService) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).InfoS("CreateVolume: called", "args", protosanitizer.StripSecrets(request))

	pvc := request.GetName()
	if len(pvc) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeName must be provided")
	}

	volCapabilities := request.GetVolumeCapabilities()
	if volCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapabilities must be provided")
	}

	params, err := ExtractParameters(request.GetParameters())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	paramsVAC, err := ExtractModifyVolumeParameters(request.GetMutableParameters())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	klog.V(5).InfoS("CreateVolume: parameters", "parameters", params, "modifyParameters", paramsVAC)

	if params.StorageID == "" {
		return nil, status.Error(codes.InvalidArgument, "parameter storage must be provided")
	}

	volSizeBytes := DefaultVolumeSizeBytes
	if request.GetCapacityRange() != nil {
		volSizeBytes = RoundUpSizeBytes(request.GetCapacityRange().GetRequiredBytes(), MinChunkSizeBytes)
	}

	accessibleTopology := request.GetAccessibilityRequirements()

	region, zone := locationFromTopologyRequirement(accessibleTopology)
	if region == "" {
		err := status.Error(codes.Internal, "cannot find best region")
		klog.ErrorS(err, "CreateVolume: region is empty", "accessibleTopology", accessibleTopology)

		return nil, err
	}

	var srcVol *volume.Volume

	contentSource := request.GetVolumeContentSource()
	if contentSource != nil {
		if contentSource.GetVolume() != nil {
			srcVol, err = volume.NewVolumeFromVolumeID(contentSource.GetVolume().GetVolumeId())
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		}

		if contentSource.GetSnapshot() != nil {
			srcVol, err = volume.NewVolumeFromVolumeID(contentSource.GetSnapshot().GetSnapshotId())
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		}
	}

	if srcVol != nil {
		if srcVol.Region() != region {
			err := status.Error(codes.InvalidArgument, "source snapshot region does not match the requested region")
			klog.ErrorS(err, "CreateVolume: source snapshot region does not match the requested region", "sourceRegion", srcVol.Region(), "requestedRegion", region)

			return nil, err
		}

		if _, err = d.checkVolume(ctx, srcVol); err != nil {
			if status.Code(err) == codes.NotFound {
				klog.ErrorS(err, "CreateVolume: zone or volume not found", "contentSourceID", srcVol.VolumeID())

				return nil, err
			}

			klog.ErrorS(err, "CreateVolume: failed to check volume", "cluster", srcVol.Cluster(), "contentSourceID", srcVol.VolumeID())

			return nil, err
		}
	}

	cl, err := d.pxpool.GetProxmoxCluster(region)
	if err != nil {
		klog.ErrorS(err, "CreateVolume: failed to get proxmox cluster", "cluster", region)

		return nil, status.Error(codes.Internal, err.Error())
	}

	if zone == "" {
		if zone, err = cl.GetNodeForStorage(ctx, params.StorageID); err != nil {
			klog.ErrorS(err, "CreateVolume: failed to get node with storage", "cluster", region, "storage", params.StorageID)

			return nil, status.Errorf(codes.Internal, "cannot find best region and zone: storage %s %v", params.StorageID, err)
		}
	}

	storageConfig, err := cl.GetClusterStorage(ctx, params.StorageID)
	if err != nil {
		klog.ErrorS(err, "CreateVolume: failed to get proxmox storage config", "cluster", region, "storage", params.StorageID)

		return nil, status.Errorf(codes.Internal, "failed to get proxmox storage config: %v", err)
	}

	topology := []*csi.Topology{
		{
			Segments: map[string]string{
				corev1.LabelTopologyRegion: region,
				corev1.LabelTopologyZone:   zone,
			},
		},
	}

	if storageConfig.Shared == 1 {
		// https://pve.proxmox.com/wiki/Storage only block/local storage are supported
		switch storageConfig.PluginType {
		case "cifs", "pbs":
			return nil, status.Error(codes.Internal, "error: shared storage type cifs, pbs are not supported")
		}

		topology = []*csi.Topology{
			{
				Segments: map[string]string{
					corev1.LabelTopologyRegion: region,
				},
			},
		}
	}

	id := vmID

	if params.Replicate {
		if storageConfig.PluginType != "zfspool" {
			return nil, status.Error(codes.Internal, "error: storage type is not zfs in replication mode")
		}

		id, err = prepareReplication(ctx, cl, zone, pvc)
		if err != nil {
			klog.ErrorS(err, "CreateVolume: failed to prepare replication", "cluster", region, "zone", zone)

			return nil, status.Error(codes.Internal, err.Error())
		}

		topology = []*csi.Topology{}

		for _, z := range strings.Split(params.ReplicateZones, ",") {
			topology = append(topology, &csi.Topology{
				Segments: map[string]string{
					corev1.LabelTopologyRegion: region,
					corev1.LabelTopologyZone:   z,
				},
			})
		}
	}

	format := ""
	if storageConfig.PluginType == "dir" {
		format = "raw"
		if params.StorageFormat == "qcow2" {
			format = params.StorageFormat
		}
	}

	vol := volume.NewVolume(region, zone, params.StorageID, fmt.Sprintf("vm-%d-%s", id, pvc), format)

	klog.V(5).InfoS("CreateVolume: creating volume", "cluster", region, "zone", zone, "volumeID", vol.VolumeID(), "size", volSizeBytes)

	size, err := getVolumeSize(ctx, cl, vol)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "CreateVolume: failed to check volume", "cluster", region, "volumeID", vol.VolumeID())

			return nil, status.Errorf(codes.Internal, "failed to check volume: %v", err)
		}

		mc := metrics.NewMetricContext("createVolume")

		if srcVol != nil {
			size, err := getVolumeSize(ctx, cl, srcVol)
			if err != nil {
				if err.Error() != ErrorNotFound {
					klog.ErrorS(err, "CreateVolume: failed to check volume", "cluster", region, "volumeID", srcVol.VolumeID())

					return nil, status.Error(codes.Internal, err.Error())
				}

				return nil, status.Errorf(codes.NotFound, "snapshot %s is not found", srcVol.VolumeID())
			}

			if size == 0 {
				return nil, status.Errorf(codes.Unavailable, "snapshot %s is not yet available", srcVol.VolumeID())
			}

			klog.V(5).InfoS("CreateVolume: creating volume from snapshot", "volumeID", vol.VolumeID(), "snapshotID", srcVol.VolumeID())

			if vol.Storage() != srcVol.Storage() {
				return nil, status.Errorf(codes.InvalidArgument, "storage mismatch: requested storage %s does not match snapshot storage %s", vol.Storage(), srcVol.Storage())
			}

			if err = copyVolume(ctx, cl, srcVol, vol); mc.ObserveRequest(err) != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
		} else {
			if err = createVolume(ctx, cl, vol, volSizeBytes); mc.ObserveRequest(err) != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
		}

		size, err = getVolumeSize(ctx, cl, vol)
		if err != nil {
			klog.ErrorS(err, "CreateVolume: failed to get volume size after creation", "cluster", region, "volumeID", vol.VolumeID())

			return nil, status.Errorf(codes.Internal, "failed to get volume size after creation: %s %s %v", vol.VolumeID(), vol.VolID(), err)
		}
	}

	if size < volSizeBytes {
		if srcVol != nil {
			if size == 0 {
				return nil, status.Errorf(codes.Unavailable, "volume %s is not yet available", srcVol.VolumeID())
			}

			params.ResizeRequired = ptr.Ptr(true)
			params.ResizeSizeBytes = volSizeBytes
		}

		if srcVol == nil {
			klog.InfoS("CreateVolume: volume has been created with different capacity", "cluster", region, "volumeID", vol.VolumeID(), "size", size, "requestedSize", volSizeBytes)
		}
	}

	volumeID := vol.VolumeID()

	if params.Replicate {
		err = createReplication(ctx, cl, id, vol, params)
		if err != nil {
			klog.ErrorS(err, "CreateVolume: failed to create replication", "cluster", region, "volumeID", vol.VolumeID(), "vmID", id)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if storageConfig.Shared == 1 || params.Replicate {
		volumeID = vol.VolumeSharedID()
	}

	klog.V(3).InfoS("CreateVolume: volume created", "cluster", vol.Cluster(), "volumeID", volumeID, "size", volSizeBytes)

	volume := csi.Volume{
		VolumeId:           volumeID,
		VolumeContext:      paramsVAC.MergeMap(params.ToMap()),
		ContentSource:      contentSource,
		CapacityBytes:      volSizeBytes,
		AccessibleTopology: topology,
	}

	return &csi.CreateVolumeResponse{Volume: &volume}, nil
}

// DeleteVolume deletes a volume.
func (d *ControllerService) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).InfoS("DeleteVolume: called", "args", protosanitizer.StripSecrets(request))

	vol, err := volume.NewVolumeFromVolumeID(request.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "DeleteVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	pv, err := d.kclient.CoreV1().PersistentVolumes().Get(ctx, vol.PV(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &csi.DeleteVolumeResponse{}, nil
		}

		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get PersistentVolumes: %v", err))
	}

	if pv.Annotations[PVAnnotationLifecycle] == "keep" {
		klog.V(3).InfoS("DeleteVolume: volume lifecycle is keep, skipping deletion", "volumeID", vol.VolumeID())

		return &csi.DeleteVolumeResponse{}, nil
	}

	err = deleteReplication(ctx, cl, vol)
	if err != nil {
		klog.ErrorS(err, "DeleteVolume: failed to delete replication", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete replication: %s, %v", vol.VolumeID(), err))
	}

	_, err = d.checkVolume(ctx, vol)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			klog.ErrorS(err, "DeleteVolume: zone or volume not found", "volumeID", vol.VolumeID())

			return &csi.DeleteVolumeResponse{}, nil
		}

		klog.ErrorS(err, "DeleteVolume: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	mc := metrics.NewMetricContext("deleteVolume")
	if err := cl.DeleteVMDisk(ctx, vmID, vol.Node(), vol.Storage(), vol.Disk()); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "DeleteVolume: failed to delete volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete volume: %s, %v", vol.VolumeID(), err))
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

	nodeID := request.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeID must be provided")
	}

	if request.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	params, err := ExtractParameters(request.GetVolumeContext())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	vol, err := volume.NewVolumeFromVolumeID(request.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	// Temporary workaround for unsafe mount, better to use a VolumeAttributesClass resource
	// It should be removed in the future, use backup=true/false in the volume attributes instead
	unsafeEnv := os.Getenv("UNSAFEMOUNT")
	if unsafeEnv == "true" { // nolint: goconst
		params.Backup = nil
	}

	if request.GetReadonly() {
		params.ReadOnly = ptr.Ptr(true)
	}

	id, _, err := d.getVMIDbyNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	size, err := d.checkVolume(ctx, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	d.vmLocks.Lock(nodeID)
	defer d.vmLocks.Unlock(nodeID)

	if params.Replicate {
		err = migrateReplication(ctx, cl, id, vol)
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: failed to migrate/sync replication", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	mc := metrics.NewMetricContext("attachVolume")

	pvInfo, err := attachVolume(ctx, cl, id, vol, params.ToCFG())
	if mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to attach volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

		return nil, status.Error(codes.Internal, err.Error())
	}

	if size < params.ResizeSizeBytes {
		klog.V(5).InfoS("ControllerPublishVolume: expandVolume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

		mc := metrics.NewMetricContext("expandVolume")

		device := deviceNamePrefix + pvInfo["lun"]
		if err = cl.ResizeVMDisk(ctx, id, vol.Node(), device, fmt.Sprintf("%dM", params.ResizeSizeBytes/MiB)); mc.ObserveRequest(err) != nil {
			klog.ErrorS(err, "ControllerPublishVolume: failed to resize vm disk", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

			return nil, status.Error(codes.Internal, err.Error())
		}

		pvInfo[resizeRequired] = "true" // nolint: goconst
	}

	klog.V(3).InfoS("ControllerPublishVolume: volume published", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "nodeID", nodeID)

	return &csi.ControllerPublishVolumeResponse{PublishContext: pvInfo}, nil
}

// ControllerUnpublishVolume unpublish a volume
func (d *ControllerService) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).InfoS("ControllerUnpublishVolume: called", "args", protosanitizer.StripSecrets(request))

	nodeID := request.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeID must be provided")
	}

	vol, err := volume.NewVolumeFromVolumeID(request.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerUnpublishVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	_, err = d.checkVolume(ctx, vol)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			klog.ErrorS(err, "ControllerUnpublishVolume: zone or volume not found", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		klog.ErrorS(err, "ControllerUnpublishVolume: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	id, _, err := d.getVMIDbyNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	d.vmLocks.Lock(nodeID)
	defer d.vmLocks.Unlock(nodeID)

	mc := metrics.NewMetricContext("detachVolume")
	if err := detachVolume(ctx, cl, id, vol); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerUnpublishVolume: failed to detach volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(3).InfoS("ControllerUnpublishVolume: volume unpublished", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "nodeID", nodeID)

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
func (d *ControllerService) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	klog.V(6).InfoS("GetCapacity: called", "args", protosanitizer.StripSecrets(request))

	topology := request.GetAccessibleTopology()
	if topology != nil {
		region, zone := getNodeTopology(topology.GetSegments())
		storageID := request.GetParameters()[StorageIDKey]

		if region == "" || storageID == "" {
			return nil, status.Error(codes.InvalidArgument, "region and storage must be provided")
		}

		cl, err := d.pxpool.GetProxmoxCluster(region)
		if err != nil {
			klog.ErrorS(err, "GetCapacity: failed to get proxmox cluster", "cluster", region)

			return nil, status.Error(codes.Internal, err.Error())
		}

		storageConfig, err := cl.GetClusterStorage(ctx, storageID)
		if err != nil {
			klog.ErrorS(err, "GetCapacity: failed to get proxmox storage config", "cluster", region, "storageID", storageID)

			return nil, status.Error(codes.Internal, err.Error())
		}

		if zone == "" {
			if storageConfig.Shared == 0 {
				return nil, status.Error(codes.InvalidArgument, "zone must be provided")
			}

			zone, err = cl.GetNodeForStorage(ctx, storageID)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
		}

		availableCapacity := int64(0)

		mc := metrics.NewMetricContext("storageStatus")

		storage, err := cl.GetStorageStatus(ctx, zone, storageID)
		if mc.ObserveRequest(err) != nil {
			klog.ErrorS(err, "GetCapacity: failed to get storage status", "cluster", region, "storageID", storageID, "storageConfig", storageConfig)

			if !strings.Contains(err.Error(), "Parameter verification failed") {
				return nil, status.Error(codes.Internal, err.Error())
			}
		} else {
			availableCapacity = int64(storage.Avail)
		}

		klog.V(6).InfoS("GetCapacity: collected", "region", region, "zone", zone, "storageID", storageID, "storageConfig", storageConfig, "size", availableCapacity)

		return &csi.GetCapacityResponse{
			AvailableCapacity: availableCapacity,
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "no topology specified")
}

// CreateSnapshot create a snapshot
func (d *ControllerService) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(4).InfoS("CreateSnapshot: called", "args", protosanitizer.StripSecrets(request))

	vol, err := volume.NewVolumeFromVolumeID(request.GetSourceVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "Name must be provided")
	}

	params := request.GetParameters()
	if params == nil {
		params = map[string]string{}
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "CreateSnapshot: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	storageConfig, err := cl.Client.ClusterStorage(ctx, vol.Storage())
	if err != nil {
		klog.ErrorS(err, "CreateSnapshot: failed to get proxmox storage config", "cluster", vol.Cluster(), "storageID", vol.Storage())

		return nil, status.Error(codes.Internal, err.Error())
	}

	switch storageConfig.Type {
	case "cifs", "pbs":
		err = status.Error(codes.Internal, "storage type cifs, pbs do not support snapshot")
		klog.ErrorS(err, "CreateSnapshot: unsupported storage type for snapshot", "cluster", vol.Cluster(), "storageID", vol.Storage(), "storageType", storageConfig.Type)

		return nil, err
	}

	_, err = d.checkVolume(ctx, vol)
	if err != nil {
		klog.ErrorS(err, "CreateSnapshot: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	snapshotID := vol.CopyVolume(fmt.Sprintf("vm-%d-%s", vmID, name))

	if params["zone"] != "" {
		if storageConfig.Nodes != "" {
			nodes := strings.Split(storageConfig.Nodes, ",")
			if !slices.Contains(nodes, params["zone"]) {
				err = status.Error(codes.InvalidArgument, "zone specified in parameters is not valid for the storage")
				klog.ErrorS(err, "CreateSnapshot: invalid zone in parameters", "cluster", vol.Cluster(), "storageID", vol.Storage(), "zone", params["zone"])

				return nil, err
			}
		}

		snapshotID.SetZone(params["zone"])
	}

	klog.V(5).InfoS("CreateSnapshot", "storageConfig", storageConfig, "snapshotID", snapshotID.VolumeID(), "params", params)

	size, err := getVolumeSize(ctx, cl, snapshotID)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "CreateSnapshot: failed to check volume", "cluster", vol.Cluster(), "snapshotID", snapshotID.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		err = copyVolume(ctx, cl, vol, snapshotID)
		if err != nil {
			klog.ErrorS(err, "CreateSnapshot: failed to create snapshot", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "snapshotID", snapshotID.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		size, err = getVolumeSize(ctx, cl, snapshotID)
		if err != nil {
			klog.ErrorS(err, "CreateSnapshot: failed to get snapshots after creation", "cluster", vol.Cluster(), "snapshotID", snapshotID.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	klog.V(3).InfoS("CreateSnapshot: snapshot created", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "snapshotID", snapshotID.VolumeID())

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			CreationTime:   timestamppb.New(time.Now()),
			SnapshotId:     snapshotID.VolumeID(),
			SourceVolumeId: vol.VolumeID(),
			SizeBytes:      size,
			ReadyToUse:     size > 0,
		},
	}, nil
}

// DeleteSnapshot delete a snapshot
func (d *ControllerService) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).InfoS("DeleteSnapshot: called", "args", protosanitizer.StripSecrets(request))

	vol, err := volume.NewVolumeFromVolumeID(request.GetSnapshotId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "DeleteSnapshot: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	_, err = d.checkVolume(ctx, vol)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			klog.ErrorS(err, "DeleteSnapshot: zone or volume not found", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

			return &csi.DeleteSnapshotResponse{}, nil
		}

		klog.ErrorS(err, "DeleteSnapshot: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	mc := metrics.NewMetricContext("deleteVolume")
	if err := cl.DeleteVMDisk(ctx, vmID, vol.Node(), vol.Storage(), vol.Disk()); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "DeleteSnapshot: failed to delete volume", "cluster", vol.Cluster(), "volumeName", vol.Disk())

		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete volume: %s", vol.Disk()))
	}

	klog.V(3).InfoS("DeleteSnapshot: snapshot deleted", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots list snapshots
func (d *ControllerService) ListSnapshots(_ context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).InfoS("ListSnapshots: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerExpandVolume expand a volume
func (d *ControllerService) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).InfoS("ControllerExpandVolume: called", "args", protosanitizer.StripSecrets(request))

	capacityRange := request.GetCapacityRange()
	if capacityRange == nil {
		return nil, status.Error(codes.InvalidArgument, "CapacityRange must be provided")
	}

	volSizeBytes := RoundUpSizeBytes(capacityRange.GetRequiredBytes(), MinChunkSizeBytes)
	maxVolSize := capacityRange.GetLimitBytes()

	if maxVolSize > 0 && maxVolSize < volSizeBytes {
		return nil, status.Error(codes.OutOfRange, "after round-up, volume size exceeds the limit specified")
	}

	vol, err := volume.NewVolumeFromVolumeID(request.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerExpandVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	_, err = d.checkVolume(ctx, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerExpandVolume: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	// FIXME: check current size and skip resize if not needed

	id, lun, err := getVMByAttachedVolume(ctx, cl, vol)
	if err != nil || id == 0 {
		if err == goproxmox.ErrNotFound {
			klog.V(3).InfoS("ControllerExpandVolume: volume is not published, cannot resize unpublished volumeID", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

			return nil, status.Error(codes.Internal, "cannot resize unpublished")
		}

		klog.ErrorS(err, "ControllerExpandVolume: failed to get vm by attached volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.Internal, err.Error())
	}

	mc := metrics.NewMetricContext("expandVolume")

	device := deviceNamePrefix + strconv.Itoa(lun)
	if err = cl.ResizeVMDisk(ctx, id, vol.Node(), device, fmt.Sprintf("%dM", volSizeBytes/MiB)); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerExpandVolume: failed to resize vm disk", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(3).InfoS("ControllerExpandVolume: volume expanded", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id, "size", volSizeBytes)

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         volSizeBytes,
		NodeExpansionRequired: true,
	}, nil
}

// ControllerGetVolume get a volume
func (d *ControllerService) ControllerGetVolume(_ context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.V(4).InfoS("ControllerGetVolume: called", "args", protosanitizer.StripSecrets(request))

	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerModifyVolume modify a volume
func (d *ControllerService) ControllerModifyVolume(ctx context.Context, request *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	klog.V(4).InfoS("ControllerModifyVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	params, err := ExtractModifyVolumeParameters(request.GetMutableParameters())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	vol, err := volume.NewVolumeFromVolumeID(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	_, err = d.checkVolume(ctx, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, err
	}

	id, _, err := getVMByAttachedVolume(ctx, cl, vol)
	if err != nil || id == 0 {
		if err == goproxmox.ErrNotFound {
			klog.V(3).InfoS("ControllerModifyVolume: volume is not published, cannot modify unpublished volumeID", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

			return nil, status.Error(codes.NotFound, "volume is not published")
		}

		klog.ErrorS(err, "ControllerModifyVolume: failed to get vm by attached volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(5).InfoS("ControllerModifyVolume: update volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id, "parameters", params.ToCFG())

	mc := metrics.NewMetricContext("updateVolume")
	if err = updateVolume(ctx, cl, id, vol, params.ToCFG()); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to update volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(3).InfoS("ControllerModifyVolume: volume modified", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", id)

	return &csi.ControllerModifyVolumeResponse{}, nil
}

func (d *ControllerService) getVMIDbyNode(ctx context.Context, nodeID string) (int, string, error) { // nolint:unparam
	node, err := d.kclient.CoreV1().Nodes().Get(ctx, nodeID, metav1.GetOptions{})
	if err != nil {
		return 0, "", status.Error(codes.InvalidArgument, err.Error())
	}

	id, err := ProxmoxVMIDbyNode(node)
	if err != nil {
		if d.Provider == csiconfig.ProviderCapmox {
			id, region, err := d.pxpool.FindVMByUUID(ctx, node.Status.NodeInfo.SystemUUID)
			if err != nil {
				return 0, "", status.Error(codes.Internal, err.Error())
			}

			return id, region, nil
		}

		klog.InfoS("failed to get proxmox VMID from ProviderID", "nodeID", nodeID, "providerID", node.Spec.ProviderID)

		id, region, err := d.pxpool.FindVMByNode(ctx, node)
		if err != nil {
			klog.ErrorS(err, "failed to get vm ref by nodeID", "nodeID", nodeID)

			return 0, "", status.Error(codes.Internal, err.Error())
		}

		return id, region, nil
	}

	return id, "", nil
}

func (d *ControllerService) checkVolume(ctx context.Context, vol *volume.Volume) (int64, error) {
	cl, err := d.pxpool.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		return 0, status.Error(codes.Internal, err.Error())
	}

	if vol.Zone() != "" {
		nodes, err := cl.GetNodeList(ctx)
		if err != nil {
			return 0, status.Error(codes.Internal, err.Error())
		}

		if !slices.Contains(nodes, vol.Zone()) {
			return 0, status.Errorf(codes.NotFound, "zone %s not found in cluster %s", vol.Zone(), vol.Cluster())
		}
	}

	if vol.Node() == "" {
		nodes, err := getNodesForStorage(ctx, cl, vol.Storage())
		if err != nil {
			return 0, status.Error(codes.Internal, err.Error())
		}

		for _, n := range nodes {
			vol.SetNode(n)

			size, err := getVolumeSize(ctx, cl, vol)
			if err != nil {
				if err.Error() == ErrorNotFound {
					continue
				}

				return 0, status.Error(codes.Internal, err.Error())
			}

			klog.V(5).InfoS("checkVolume: determined node for volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "node", n)

			return size, nil
		}

		return 0, status.Errorf(codes.NotFound, "volume %s not found in any node for storage %s", vol.VolumeID(), vol.Storage())

		// node, err := getNodeForVolume(ctx, cl, vol)
		// if err != nil {
		// 	return 0, status.Error(codes.Internal, err.Error())
		// }
		// vol.SetNode(node)
	}

	size, err := getVolumeSize(ctx, cl, vol)
	if err != nil {
		if err.Error() == ErrorNotFound {
			return 0, status.Errorf(codes.NotFound, "volume %s not found", vol.VolumeID())
		}

		return 0, status.Error(codes.Internal, err.Error())
	}

	return size, nil
}
