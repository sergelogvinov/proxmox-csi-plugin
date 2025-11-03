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

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	csiconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/helpers/ptr"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/metrics"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmox"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
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

	Cluster  *pxpool.ProxmoxPool
	Kclient  clientkubernetes.Interface
	Provider csiconfig.Provider

	vmLocks *proxmox.VMLocks
}

// NewControllerService returns a new controller service
func NewControllerService(kclient *clientkubernetes.Clientset, cloudConfig string) (*ControllerService, error) {
	cfg, err := csiconfig.ReadCloudConfigFromFile(cloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	cluster, err := pxpool.NewProxmoxPool(cfg.Clusters, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxmox cluster client: %v", err)
	}

	d := &ControllerService{
		Cluster:  cluster,
		Kclient:  kclient,
		Provider: cfg.Features.Provider,
	}

	d.Init()

	return d, nil
}

// Init initializes the controller service
func (d *ControllerService) Init() {
	if d.vmLocks == nil {
		d.vmLocks = proxmox.NewVMLocks()
	}
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

	params, err := ExtractAndDefaultParameters(request.GetParameters())
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
	}

	cl, err := d.Cluster.GetProxmoxCluster(region)
	if err != nil {
		klog.ErrorS(err, "CreateVolume: failed to get proxmox cluster", "cluster", region)

		return nil, status.Error(codes.Internal, err.Error())
	}

	if zone == "" {
		if zone, err = getNodeWithStorage(cl, params.StorageID); err != nil {
			klog.ErrorS(err, "CreateVolume: failed to get node with storage", "cluster", region, "storage", params.StorageID)

			return nil, status.Errorf(codes.Internal, "cannot find best region and zone: %v", err)
		}
	}

	storageConfig, err := cl.GetStorageConfig(params.StorageID)
	if err != nil {
		klog.ErrorS(err, "CreateVolume: failed to get proxmox storage config", "cluster", region, "storage", params.StorageID)

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(5).InfoS("CreateVolume", "storageConfig", storageConfig)

	topology := []*csi.Topology{
		{
			Segments: map[string]string{
				corev1.LabelTopologyRegion: region,
				corev1.LabelTopologyZone:   zone,
			},
		},
	}

	if storageConfig["shared"] != nil && int(storageConfig["shared"].(float64)) == 1 { //nolint:errcheck
		// https://pve.proxmox.com/wiki/Storage only block/local storage are supported
		switch storageConfig["type"].(string) { //nolint:errcheck
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

	vmr := pxapi.NewVmRef(vmID)
	vmr.SetNode(zone)
	vmr.SetVmType("qemu")

	if params.Replicate != nil && *params.Replicate {
		if storageConfig["type"].(string) != "zfspool" { //nolint:errcheck
			return nil, status.Error(codes.Internal, "error: storage type is not zfs in replication mode")
		}

		vmr, err = cl.GetVmRefByName(pvc)
		if err != nil {
			id, err := cl.GetNextID(vmID + 1)
			if err != nil {
				klog.ErrorS(err, "CreateVolume: failed to get next id", "cluster", region)

				return nil, status.Error(codes.Internal, err.Error())
			}

			vmr = pxapi.NewVmRef(id)
			vmr.SetNode(zone)
			vmr.SetVmType("qemu")

			mc := metrics.NewMetricContext("CreateVm")
			if err := proxmox.CreateQemuVM(cl, vmr, pvc); mc.ObserveRequest(err) != nil {
				klog.ErrorS(err, "CreateVolume: failed to create vm", "cluster", region)

				return nil, status.Error(codes.Internal, err.Error())
			}
		}
	}

	vol := volume.NewVolume(region, zone, params.StorageID, fmt.Sprintf("vm-%d-%s", vmr.VmId(), pvc))

	if storageConfig["path"] != nil && storageConfig["path"].(string) != "" { //nolint:errcheck
		format := "raw"
		if params.StorageFormat == "qcow2" {
			format = params.StorageFormat
		}

		vol = volume.NewVolume(region, zone, params.StorageID, fmt.Sprintf("%d/vm-%d-%s.%s", vmr.VmId(), vmr.VmId(), pvc, format))
	}

	size, err := getVolumeSize(cl, vol)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "CreateVolume: failed to check volume", "cluster", region, "volumeID", vol.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		mc := metrics.NewMetricContext("createVolume")

		if srcVol != nil {
			size, err := getVolumeSize(cl, srcVol)
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

			err = proxmox.CopyQemuDisk(cl, srcVol, vol)
			if mc.ObserveRequest(err) != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
		} else {
			err = createVolume(cl, vol, volSizeBytes)
			if mc.ObserveRequest(err) != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
		}

		size, err = getVolumeSize(cl, vol)
		if err != nil {
			klog.ErrorS(err, "CreateVolume: failed to get volume size after creation", "cluster", region, "volumeID", vol.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if size != volSizeBytes {
		if srcVol != nil {
			if size == 0 {
				return nil, status.Errorf(codes.Unavailable, "volume %s is not yet available", srcVol.VolumeID())
			}

			if size < volSizeBytes {
				params.ResizeRequired = ptr.Ptr(true)
				params.ResizeSizeBytes = volSizeBytes
			}
		}

		if srcVol == nil {
			klog.InfoS("CreateVolume: volume has been created with different capacity", "cluster", region, "volumeID", vol.VolumeID(), "size", size, "requestedSize", volSizeBytes)
		}
	}

	volumeID := vol.VolumeID()

	if params.Replicate != nil && *params.Replicate {
		_, err := attachVolume(cl, vmr, vol.Storage(), vol.Disk(), params.ToMap())
		if err != nil {
			klog.ErrorS(err, "CreateVolume: failed to attach volume", "cluster", region, "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

			return nil, status.Error(codes.Internal, err.Error())
		}

		if params.ReplicateZones != "" {
			var replicaZone string

			for _, z := range strings.Split(params.ReplicateZones, ",") {
				if z != zone {
					replicaZone = z

					break
				}
			}

			if replicaZone != "" {
				if err := proxmox.SetQemuVMReplication(cl, vmr, replicaZone, params.ReplicateSchedule); err != nil {
					klog.ErrorS(err, "CreateVolume: failed to set replication", "cluster", region, "zone", replicaZone, "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

					return nil, status.Error(codes.Internal, err.Error())
				}

				volumeID = vol.VolumeSharedID()
				topology = []*csi.Topology{
					{
						Segments: map[string]string{
							corev1.LabelTopologyRegion: region,
							corev1.LabelTopologyZone:   zone,
						},
					},
					{
						Segments: map[string]string{
							corev1.LabelTopologyRegion: region,
							corev1.LabelTopologyZone:   replicaZone,
						},
					},
				}
			}
		}
	}

	if storageConfig["shared"] != nil && int(storageConfig["shared"].(float64)) == 1 { //nolint:errcheck
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

	pv, err := d.Kclient.CoreV1().PersistentVolumes().Get(ctx, vol.PV(), metav1.GetOptions{})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get PersistentVolumes: %v", err))
	}

	if pv.Annotations[PVAnnotationLifecycle] == "keep" {
		klog.V(2).InfoS("DeleteVolume: volume lifecycle is keep, skipping deletion", "volumeID", vol.VolumeID())

		return &csi.DeleteVolumeResponse{}, nil
	}

	if vol.Zone() != "" {
		nodes, err := proxmox.GetNodeList(cl)
		if err != nil {
			klog.ErrorS(err, "DeleteVolume: failed to get node list in cluster", "cluster", vol.Cluster())

			return nil, status.Error(codes.Internal, err.Error())
		}

		if !slices.Contains(nodes, vol.Zone()) {
			klog.V(3).InfoS("DeleteVolume: zone does not exist", "volumeID", vol.VolumeID(), "zone", vol.Zone())

			return &csi.DeleteVolumeResponse{}, nil
		}
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

	if vmr.VmId() != vmID {
		config, err := cl.GetVmConfig(vmr)
		if err != nil {
			klog.ErrorS(err, "DeleteVolume: failed to get vm config", "cluster", vol.Cluster(), "volumeName", vol.Disk())
		}

		if config != nil {
			vmName := config["name"].(string) //nolint:errcheck
			if vmName != "" && strings.HasSuffix(vol.Disk(), vmName) {
				mc := metrics.NewMetricContext("deleteVm")
				if err := proxmox.DeleteQemuVM(cl, vmr); mc.ObserveRequest(err) != nil {
					klog.ErrorS(err, "DeleteVolume: failed to delete vm", "cluster", vol.Cluster(), "volumeName", vol.Disk())

					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete volume: %s", vol.Disk()))
				}
			}

			mc := metrics.NewMetricContext("deleteDisk")
			if err = proxmox.DeleteDisk(cl, vol); mc.ObserveRequest(err) != nil {
				klog.ErrorS(err, "DeleteVolume: failed to delete disk", "cluster", vol.Cluster(), "volumeName", vol.Disk())
			}
		}
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

	params, err := ExtractAndDefaultParameters(volCtx)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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

	// Temporary workaround for unsafe mount, better to use a VolumeAttributesClass resource
	// It should be removed in the future, use backup=true/false in the volume attributes instead
	unsafeEnv := os.Getenv("UNSAFEMOUNT")
	if unsafeEnv == "true" { // nolint: goconst
		params.Backup = nil
	}

	if request.GetReadonly() {
		params.ReadOnly = ptr.Ptr(true)
	}

	vmr, err := d.getVMRefbyNodeID(ctx, cl, nodeID)
	if err != nil {
		return nil, err
	}

	size, err := getVolumeSize(cl, vol)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "ControllerPublishVolume: failed to check volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		return nil, status.Error(codes.NotFound, "volume not found")
	}

	d.vmLocks.Lock(nodeID)
	defer d.vmLocks.Unlock(nodeID)

	if params.Replicate != nil && *params.Replicate {
		vmrVol, err := getVMRefByVolume(cl, vol)
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: failed to get vm ref by volume", "cluster", vol.Cluster(), "volumeName", vol.Disk())

			return nil, status.Error(codes.Internal, err.Error())
		}

		if vmr.Node() != vmrVol.Node() {
			klog.V(4).InfoS("ControllerPublishVolume: replicate volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "src", vmrVol.Node(), "dst", vmr.Node())

			_, err := cl.MigrateNode(vmrVol, vmr.Node(), false)
			if err != nil {
				klog.ErrorS(err, "ControllerPublishVolume: failed to migrate vm", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

				return nil, status.Error(codes.Internal, err.Error())
			}
		}
	}

	mc := metrics.NewMetricContext("attachVolume")

	pvInfo, err := attachVolume(cl, vmr, vol.Storage(), vol.Disk(), params.ToMap())
	if mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerPublishVolume: failed to attach volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

		return nil, status.Error(codes.Internal, err.Error())
	}

	if resizeSizeBytesRaw := volCtx[resizeSizeBytes]; resizeSizeBytesRaw != "" {
		resizeSizeBytes, err := strconv.ParseInt(resizeSizeBytesRaw, 10, 64)
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: invalid resize size", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())
		}

		if size < resizeSizeBytes {
			device := deviceNamePrefix + pvInfo["lun"]

			klog.V(3).InfoS("ControllerPublishVolume: expandVolume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

			mc := metrics.NewMetricContext("expandVolume")

			if _, err := cl.ResizeQemuDiskRaw(vmr, device, fmt.Sprintf("%dM", resizeSizeBytes/MiB)); mc.ObserveRequest(err) != nil {
				klog.ErrorS(err, "ControllerExpandVolume: failed to resize vm disk", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

				return nil, status.Error(codes.Internal, err.Error())
			}
		}
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

	if vol.Zone() != "" {
		nodes, err := proxmox.GetNodeList(cl)
		if err != nil {
			klog.ErrorS(err, "ControllerUnpublishVolume: failed to get node list in cluster", "cluster", vol.Cluster())

			return nil, status.Error(codes.Internal, err.Error())
		}

		if !slices.Contains(nodes, vol.Zone()) {
			klog.V(3).InfoS("ControllerUnpublishVolume: zone does not exist", "volumeID", vol.VolumeID(), "zone", vol.Zone())

			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
	}

	vmr, err := d.getVMRefbyNodeID(ctx, cl, nodeID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			klog.V(3).InfoS("ControllerUnpublishVolume: node does not exist", "volumeID", vol.VolumeID(), "nodeID", nodeID)

			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		return nil, err
	}

	d.vmLocks.Lock(nodeID)
	defer d.vmLocks.Unlock(nodeID)

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
	klog.V(6).InfoS("GetCapacity: called", "args", protosanitizer.StripSecrets(request))

	topology := request.GetAccessibleTopology()
	if topology != nil {
		region, zone := getNodeTopology(topology.GetSegments())
		storageID := request.GetParameters()[StorageIDKey]

		if region == "" || storageID == "" {
			return nil, status.Error(codes.InvalidArgument, "region and storage must be provided")
		}

		cl, err := d.Cluster.GetProxmoxCluster(region)
		if err != nil {
			klog.ErrorS(err, "GetCapacity: failed to get proxmox cluster", "cluster", region)

			return nil, status.Error(codes.Internal, err.Error())
		}

		storageConfig, err := cl.GetStorageConfig(storageID)
		if err != nil {
			klog.ErrorS(err, "GetCapacity: failed to get proxmox storage config", "cluster", region, "storageID", storageID)

			return nil, status.Error(codes.Internal, err.Error())
		}

		shared := 0
		if storageConfig["shared"] != nil && int(storageConfig["shared"].(float64)) == 1 { //nolint:errcheck
			shared = 1
		}

		if zone == "" {
			if shared == 0 {
				return nil, status.Error(codes.InvalidArgument, "zone must be provided")
			}

			zone, err = getNodeWithStorage(cl, storageID)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
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
			availableCapacity = int64(storage["avail"].(float64)) //nolint:errcheck
		}

		klog.V(6).InfoS("GetCapacity: collected", "region", region, "zone", zone, "storageID", storageID, "shared", shared, "size", availableCapacity)

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

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "CreateSnapshot: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	storageConfig, err := cl.GetStorageConfig(vol.Storage())
	if err != nil {
		klog.ErrorS(err, "CreateSnapshot: failed to get proxmox storage config", "cluster", vol.Cluster(), "storageID", vol.Storage())

		return nil, status.Error(codes.Internal, err.Error())
	}

	snapshotID := volume.NewVolume(vol.Region(), vol.Zone(), vol.Storage(), fmt.Sprintf("vm-%s-%s", vol.VMID(), name))

	if params["zone"] != "" {
		if nodesRaw, ok := storageConfig["nodes"].(string); ok && nodesRaw != "" {
			nodes := strings.Split(nodesRaw, ",")
			if !slices.Contains(nodes, params["zone"]) {
				err = status.Error(codes.InvalidArgument, "zone specified in parameters is not valid for the storage")
				klog.ErrorS(err, "CreateSnapshot: invalid zone in parameters", "cluster", vol.Cluster(), "storageID", vol.Storage(), "zone", params["zone"])

				return nil, err
			}
		}

		snapshotID = volume.NewVolume(vol.Region(), params["zone"], vol.Storage(), fmt.Sprintf("vm-%s-%s", vol.VMID(), name))
	}

	klog.V(5).InfoS("CreateSnapshot", "storageConfig", storageConfig, "snapshotID", snapshotID.VolumeID(), "params", params)

	size, err := getVolumeSize(cl, snapshotID)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "CreateSnapshot: failed to check volume", "cluster", vol.Cluster(), "snapshotID", snapshotID.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		err = proxmox.CopyQemuDisk(cl, vol, snapshotID)
		if err != nil {
			klog.ErrorS(err, "CreateSnapshot: failed to create snapshot", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "snapshotID", snapshotID.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		size, err = getVolumeSize(cl, snapshotID)
		if err != nil {
			klog.ErrorS(err, "CreateSnapshot: failed to get snapshots after creation", "cluster", vol.Cluster(), "snapshotID", snapshotID.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

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
func (d *ControllerService) DeleteSnapshot(_ context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).InfoS("DeleteSnapshot: called", "args", protosanitizer.StripSecrets(request))

	vol, err := volume.NewVolumeFromVolumeID(request.GetSnapshotId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "DeleteSnapshot: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	_, err = getVolumeSize(cl, vol)
	if err != nil {
		if err.Error() != ErrorNotFound {
			klog.ErrorS(err, "DeleteSnapshot: failed to get snapshots", "cluster", vol.Cluster(), "snapshotID", vol.VolumeID())

			return nil, status.Error(codes.Internal, err.Error())
		}

		return &csi.DeleteSnapshotResponse{}, nil
	}

	vmr, err := getVMRefByVolume(cl, vol)
	if err != nil {
		klog.ErrorS(err, "DeleteSnapshot: failed to get vm ref by volume", "cluster", vol.Cluster(), "volumeName", vol.Disk())

		return nil, status.Error(codes.Internal, err.Error())
	}

	mc := metrics.NewMetricContext("deleteVolume")
	if _, err := cl.DeleteVolume(vmr, vol.Storage(), vol.Disk()); mc.ObserveRequest(err) != nil {
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

	volSizeBytes := RoundUpSizeBytes(capacityRange.GetRequiredBytes(), MinChunkSizeBytes)
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

	if vol.Zone() != "" {
		nodes, err := proxmox.GetNodeList(cl)
		if err != nil {
			klog.ErrorS(err, "ControllerExpandVolume: failed to get node list in cluster", "cluster", vol.Cluster())

			return nil, status.Error(codes.Internal, err.Error())
		}

		if !slices.Contains(nodes, vol.Zone()) {
			klog.V(3).InfoS("ControllerExpandVolume: zone does not exist", "volumeID", vol.VolumeID(), "zone", vol.Zone())

			return &csi.ControllerExpandVolumeResponse{}, nil
		}
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
		err = status.Error(codes.Internal, fmt.Sprintf("failed to cast response to list, vmlist: %v", vmlist))

		return nil, err
	}

	for vmii := range vms {
		vm, ok := vms[vmii].(map[string]interface{})
		if !ok {
			return nil, status.Errorf(codes.Internal, "failed to cast response to map, vm: %v", vm)
		}

		if vmType, ok := vm["type"].(string); ok && vmType != "qemu" {
			klog.V(5).InfoS("ControllerExpandVolume: skipping non-qemu VM", "VM", vm["name"]) //nolint:errcheck

			continue
		}

		if node, ok := vm["node"].(string); ok && node == vol.Node() || vol.Node() == "" {
			vmID := int(vm["vmid"].(float64)) //nolint:errcheck

			vmr := pxapi.NewVmRef(vmID)
			vmr.SetNode(vol.Node())
			vmr.SetVmType("qemu")

			if vmr.Node() == "" {
				vmr.SetNode(node)
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

			if _, err := cl.ResizeQemuDiskRaw(vmr, device, fmt.Sprintf("%dM", volSizeBytes/MiB)); mc.ObserveRequest(err) != nil {
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

	volumeID := request.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	paramsVAC, err := ExtractModifyVolumeParameters(request.GetMutableParameters())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	klog.V(5).InfoS("ControllerModifyVolume: modify parameters", "parameters", paramsVAC)

	vol, err := volume.NewVolumeFromVolumeID(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cl, err := d.Cluster.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to get proxmox cluster", "cluster", vol.Cluster())

		return nil, status.Error(codes.Internal, err.Error())
	}

	exist, err := isPvcExists(cl, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to verify the existence of the PVC", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return nil, status.Error(codes.NotFound, err.Error())
	}

	if !exist {
		klog.V(3).InfoS("ControllerModifyVolume: volume not found", "cluster", vol.Cluster(), "volumeID", vol.VolumeID())

		return &csi.ControllerModifyVolumeResponse{}, nil
	}

	vmr, err := getVMRefByAttachedVolume(cl, vol)
	if err != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to get vm ref by volume", "cluster", vol.Cluster(), "volumeName", vol.Disk())

		return nil, status.Error(codes.NotFound, err.Error())
	}

	klog.V(5).InfoS("ControllerModifyVolume: update volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId(), "parameters", paramsVAC.ToMap())

	mc := metrics.NewMetricContext("updateVolume")
	if err = updateVolume(cl, vmr, vol.Storage(), vol.Disk(), paramsVAC.ToMap()); mc.ObserveRequest(err) != nil {
		klog.ErrorS(err, "ControllerModifyVolume: failed to update volume", "cluster", vol.Cluster(), "volumeID", vol.VolumeID(), "vmID", vmr.VmId())

		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.ControllerModifyVolumeResponse{}, nil
}

func (d *ControllerService) getVMRefbyNodeID(ctx context.Context, cl *pxapi.Client, nodeID string) (*pxapi.VmRef, error) {
	var vmr *pxapi.VmRef

	node, err := d.Kclient.CoreV1().Nodes().Get(ctx, nodeID, metav1.GetOptions{})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if d.Provider == csiconfig.ProviderCapmox {
		vmr, _, err = d.Cluster.FindVMByUUID(ctx, node.Status.NodeInfo.SystemUUID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		return vmr, nil
	}

	vmrid, err := tools.ProxmoxVMIDbyNode(ctx, node)
	if err != nil {
		klog.InfoS("ControllerPublishVolume: failed to get proxmox vmrID from ProviderID", "nodeID", nodeID)

		vmr, err = cl.GetVmRefByName(nodeID)
		if err != nil {
			klog.ErrorS(err, "ControllerPublishVolume: failed to get vm ref by nodeID", "nodeID", nodeID)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if vmr == nil {
		_, zone := getNodeTopology(node.Labels)

		vmr = pxapi.NewVmRef(vmrid)
		vmr.SetNode(zone)
		vmr.SetVmType("qemu")
	}

	return vmr, nil
}
