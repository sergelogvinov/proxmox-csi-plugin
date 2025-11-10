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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/siderolabs/go-blockdevice/blockdevice/encryption"
	luks "github.com/siderolabs/go-blockdevice/blockdevice/encryption/luks"
	"github.com/siderolabs/go-blockdevice/blockdevice/filesystem"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/cloud-provider-openstack/pkg/util/blockdevice"
	"k8s.io/cloud-provider-openstack/pkg/util/mount"
	"k8s.io/klog/v2"
	mountutil "k8s.io/mount-utils"
	"k8s.io/utils/exec"
	utilpath "k8s.io/utils/path"
)

var nodeCaps = []csi.NodeServiceCapability_RPC_Type{
	csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
	csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
}

var volumeCaps = []csi.VolumeCapability_AccessMode_Mode{
	csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
}

const (
	// VolumesPerNodeHardLimit is the technical limitation of the number of volumes that can be attached to a single node.
	// This is currently limited by the QEMU limit of 30 iscsi volumes, see `man qm` for details.`
	VolumesPerNodeHardLimit = 30
)

// NodeService is the node service for the CSI driver
type NodeService struct {
	csi.UnimplementedNodeServer

	nodeID  string
	kclient kubernetes.Interface

	Mount       mount.IMount
	volumeLocks sync.Mutex
}

// NewNodeService returns a new NodeService
func NewNodeService(nodeID string, clientSet kubernetes.Interface) *NodeService {
	return &NodeService{
		nodeID:  nodeID,
		kclient: clientSet,
		Mount:   mount.GetMountProvider(),
	}
}

// NodeStageVolume is called by the CO when a workload that wants to use the specified volume is placed (scheduled) on a node.
//
//nolint:cyclop,gocyclo
func (n *NodeService) NodeStageVolume(_ context.Context, request *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.V(4).InfoS("NodeStageVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	stagingTarget := request.GetStagingTargetPath()
	if len(stagingTarget) == 0 {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath must be provided")
	}

	publishContext := request.GetPublishContext()
	if len(publishContext) == 0 {
		return nil, status.Error(codes.InvalidArgument, "PublishContext must be provided")
	}

	volumeCapability := request.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	params, err := ExtractParameters(request.GetVolumeContext())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	devicePath, err := getDevicePath(request.GetPublishContext())
	if err != nil {
		klog.ErrorS(err, "NodePublishVolume: failed to get device path", "context", request.GetPublishContext())

		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if blk := volumeCapability.GetBlock(); blk != nil {
		klog.V(3).InfoS("NodeStageVolume: raw device, skipped", "device", devicePath)

		return &csi.NodeStageVolumeResponse{}, nil
	}

	klog.V(5).InfoS("NodeStageVolume: mount device", "device", devicePath, "path", stagingTarget)

	n.volumeLocks.Lock()
	defer n.volumeLocks.Unlock()

	m := n.Mount

	requiredResize, _ := strconv.ParseBool(publishContext[resizeRequired]) // nolint:errcheck

	notMnt, err := m.IsLikelyNotMountPointAttach(stagingTarget)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if notMnt {
		options := []string{}
		fsType := FSTypeExt4

		if mnt := volumeCapability.GetMount(); mnt != nil {
			if mnt.GetFsType() != "" {
				fsType = mnt.GetFsType()
			}

			options = collectMountOptions(params, fsType, mnt.GetMountFlags())
		}

		formatOptions := collectFormatOptions(params, fsType)

		passphraseKey, ok := request.GetSecrets()[EncryptionPassphraseKey]
		if ok {
			klog.V(5).InfoS("NodeStageVolume: volume is encrypted", "device", devicePath)

			sb, err := filesystem.Probe(devicePath) //nolint:govet
			if err != nil {
				klog.ErrorS(err, "NodeStageVolume: failed to probe filesystem for device", "device", devicePath)
			}

			key := encryption.NewKey(encryption.AnyKeyslot, []byte(passphraseKey))
			l := luks.New(luks.AESXTSPlain64Cipher)

			if sb == nil {
				if err = l.Encrypt(devicePath, key); err != nil {
					klog.ErrorS(err, "NodeStageVolume: failed to encrypt device", "device", devicePath)

					return nil, status.Error(codes.Internal, err.Error())
				}
			}

			if requiredResize {
				if err := l.Resize(devicePath, key); err != nil {
					klog.ErrorS(err, "NodeStageVolume: failed to resize encrypted volume", "device", devicePath)

					return nil, status.Errorf(codes.Internal, "Could not resize encrypted volume %s failed with error %v", devicePath, err)
				}
			}

			lukskDevicePath, err := l.Open(devicePath, key) //nolint:govet
			if err != nil {
				klog.ErrorS(err, "NodeStageVolume: failed to open encrypted device", "device", devicePath)

				return nil, status.Error(codes.Internal, err.Error())
			}

			devicePath = lukskDevicePath
		}

		klog.V(5).InfoS("NodeStageVolume: mount device with options", "device", devicePath, "fsType", fsType, "options", options, "formatOptions", formatOptions)

		err = m.Mounter().FormatAndMountSensitiveWithFormatOptions(devicePath, stagingTarget, fsType, options, nil, formatOptions)
		if err != nil {
			klog.ErrorS(err, "NodeStageVolume: failed to mount device", "device", devicePath)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if requiredResize {
		klog.V(5).InfoS("NodeStageVolume: resizing volume created from a snapshot/volume", "volumeID", volumeID)

		r := mountutil.NewResizeFs(n.Mount.Mounter().Exec)
		if _, err := r.Resize(devicePath, stagingTarget); err != nil {
			return nil, status.Errorf(codes.Internal, "Could not resize volume %q:  %v", volumeID, err)
		}
	}

	klog.V(3).InfoS("NodeStageVolume: volume mounted", "device", devicePath, "resized", requiredResize)

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume is called by the CO when a workload that was using the specified volume is being moved to a different node.
//
//nolint:dupl
func (n *NodeService) NodeUnstageVolume(_ context.Context, request *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(4).InfoS("NodeUnstageVolume: called", "args", protosanitizer.StripSecrets(request))

	stagingTargetPath := request.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath must be provided")
	}

	// Raw Block device is not mounted, so we can return here
	// https://github.com/kubernetes/kubernetes/blob/master/pkg/volume/csi/csi_block.go
	if strings.Contains(stagingTargetPath, "/kubernetes.io/csi/volumeDevices/") {
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	n.volumeLocks.Lock()
	defer n.volumeLocks.Unlock()

	cmd := exec.New().Command("fstrim", "-v", stagingTargetPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		klog.ErrorS(err, "NodeUnstageVolume: failed to trim filesystem", "path", stagingTargetPath)
	} else {
		klog.V(5).InfoS("NodeUnstageVolume: fstrim", "output", string(out))
	}

	sourcePath, err := n.Mount.GetMountFs(stagingTargetPath)
	if err != nil {
		klog.ErrorS(err, "NodeUnstageVolume: failed to find mount file system", "path", stagingTargetPath)
	}

	if err = n.Mount.UnmountPath(stagingTargetPath); err != nil {
		klog.ErrorS(err, "NodeUnstageVolume: failed to unmount targetPath", "path", stagingTargetPath)

		return nil, status.Errorf(codes.Internal, "Unmount of targetPath %s failed with error %v", stagingTargetPath, err)
	}

	// wait fsync to complete
	time.Sleep(3 * time.Second)

	devicePath := strings.TrimSpace(string(sourcePath))
	if strings.HasPrefix(devicePath, "/dev/mapper/") {
		l := luks.New(luks.AESXTSPlain64Cipher)
		if err = l.Close(devicePath); err != nil {
			klog.ErrorS(err, "NodeUnstageVolume: failed to close encrypted device", "device", devicePath)

			return nil, status.Errorf(codes.Internal, "Close encrypted device %s failed with error %v", devicePath, err)
		}
	} else {
		deviceName := filepath.Base(devicePath)

		if err = os.WriteFile(fmt.Sprintf("/sys/block/%s/device/state", deviceName), []byte("offline"), 0644); err != nil { //nolint:gofumpt
			klog.InfoS("NodeUnstageVolume: failed to offline device, ignored", "device", devicePath)
		}
	}

	klog.V(3).InfoS("NodeUnstageVolume: volume unmouned", "device", devicePath)

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume mounts the volume on the node.
//
//nolint:dupl
func (n *NodeService) NodePublishVolume(_ context.Context, request *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(4).InfoS("NodePublishVolume: called", "args", protosanitizer.StripSecrets(request))

	stagingTargetPath := request.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath must be provided")
	}

	targetPath := request.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "TargetPath must be provided")
	}

	volumeCapability := request.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	if !isValidVolumeCapabilities([]*csi.VolumeCapability{volumeCapability}) {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability not supported")
	}

	devicePath := request.GetPublishContext()["DevicePath"]
	if len(devicePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "DevicePath must be provided")
	}

	mountOptions := []string{"bind"}
	if request.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	} else {
		mountOptions = append(mountOptions, "rw")
	}

	m := n.Mount

	if blk := volumeCapability.GetBlock(); blk != nil {
		podVolumePath := filepath.Dir(targetPath)

		exists, err := utilpath.Exists(utilpath.CheckFollowSymlink, podVolumePath)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		if !exists {
			if err = m.MakeDir(podVolumePath); err != nil {
				return nil, status.Errorf(codes.Internal, "Could not create dir %q: %v", podVolumePath, err)
			}
		}

		err = m.MakeFile(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Error in making file %v", err)
		}

		if err := m.Mounter().Mount(devicePath, targetPath, "", mountOptions); err != nil {
			if removeErr := os.Remove(targetPath); removeErr != nil {
				return nil, status.Errorf(codes.Internal, "Could not remove mount target %q: %v", targetPath, err)
			}

			return nil, status.Errorf(codes.Internal, "Could not mount %q at %q: %v", devicePath, targetPath, err)
		}

		return &csi.NodePublishVolumeResponse{}, nil
	}

	_, err := m.GetMountFs(stagingTargetPath)
	if err != nil {
		klog.ErrorS(err, "NodePublishVolume: stage volume is not mounted", "path", stagingTargetPath)

		return nil, status.Error(codes.NotFound, fmt.Sprintf("Failed to find mount file system %s: %v", stagingTargetPath, err))
	}

	notMnt, err := m.IsLikelyNotMountPointAttach(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if notMnt {
		fsType := "ext4"

		if mnt := volumeCapability.GetMount(); mnt != nil {
			if mnt.GetFsType() != "" {
				fsType = mnt.GetFsType()
			}
		}

		err = m.Mounter().Mount(stagingTargetPath, targetPath, fsType, mountOptions)
		if err != nil {
			klog.ErrorS(err, "NodePublishVolume: error mounting volume", "stagingPath", stagingTargetPath, "path", targetPath)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	klog.V(3).InfoS("NodePublishVolume: volume published for pod", "device", devicePath,
		"pod", klog.KRef(request.GetVolumeContext()["csi.storage.k8s.io/pod.namespace"], request.GetVolumeContext()["csi.storage.k8s.io/pod.name"]),
	)

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume from the target path
//
//nolint:dupl
func (n *NodeService) NodeUnpublishVolume(_ context.Context, request *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(4).InfoS("NodeUnpublishVolume: called", "args", protosanitizer.StripSecrets(request))

	targetPath := request.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "TargetPath must be provided")
	}

	err := n.Mount.UnmountPath(targetPath)
	if err != nil {
		klog.ErrorS(err, "NodeUnpublishVolume: error unmounting volume", "path", targetPath)

		return nil, status.Errorf(codes.Internal, "Unmount of targetpath %s failed with error %v", targetPath, err)
	}

	klog.V(3).InfoS("NodePublishVolume: volume unpublished", "path", targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats get the volume stats
func (n *NodeService) NodeGetVolumeStats(_ context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(5).InfoS("NodeGetVolumeStats: called", "args", protosanitizer.StripSecrets(request))

	volumePath := request.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumePath must be provided")
	}

	exists, err := utilpath.Exists(utilpath.CheckFollowSymlink, request.GetVolumePath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to check whether volumePath exists: %s", err)
	}

	if !exists {
		return nil, status.Errorf(codes.NotFound, "target: %s not found", volumePath)
	}

	stats, err := n.Mount.GetDeviceStats(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get stats by path: %s", err)
	}

	if stats.Block {
		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				{
					Total: stats.TotalBytes,
					Unit:  csi.VolumeUsage_BYTES,
				},
			},
		}, nil
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{Total: stats.TotalBytes, Available: stats.AvailableBytes, Used: stats.UsedBytes, Unit: csi.VolumeUsage_BYTES},
			{Total: stats.TotalInodes, Available: stats.AvailableInodes, Used: stats.UsedInodes, Unit: csi.VolumeUsage_INODES},
		},
	}, nil
}

// NodeExpandVolume expand the volume
func (n *NodeService) NodeExpandVolume(_ context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	klog.V(4).InfoS("NodeExpandVolume: called", "args", protosanitizer.StripSecrets(request))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	volumePath := request.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumePath must be provided")
	}

	volCapability := request.GetVolumeCapability()
	if volCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	if volCapability.GetBlock() != nil {
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	output, err := n.Mount.GetMountFs(volumePath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to find mount file system %s: %v", volumePath, err))
	}

	devicePath := strings.TrimSpace(string(output))
	if devicePath == "" {
		return nil, status.Error(codes.Internal, "Unable to find Device path for volume")
	}

	if strings.HasPrefix(devicePath, "/dev/mapper/") {
		passphraseKey, ok := request.GetSecrets()[EncryptionPassphraseKey]
		if !ok {
			klog.ErrorS(err, "NodeExpandVolume: failed to resize encrypted volume, check feature gate CSINodeExpandSecret", "device", devicePath)

			return nil, status.Errorf(codes.InvalidArgument, "Could not resize encrypted volume %s passphrase key is empty", devicePath)
		}

		key := encryption.NewKey(encryption.AnyKeyslot, []byte(passphraseKey))
		l := luks.New(luks.AESXTSPlain64Cipher)

		if err := l.Resize(devicePath, key); err != nil {
			klog.ErrorS(err, "NodeExpandVolume: failed to resize encrypted volume", "device", devicePath)

			return nil, status.Errorf(codes.Internal, "Could not resize encrypted volume %s failed with error %v", devicePath, err)
		}
	} else {
		// comparing current volume size with the expected one
		newSize := request.GetCapacityRange().GetRequiredBytes()
		if err := blockdevice.RescanBlockDeviceGeometry(devicePath, volumePath, newSize); err != nil {
			return nil, status.Errorf(codes.Internal, "Could not verify %q volume size: %v", volumeID, err)
		}
	}

	r := mountutil.NewResizeFs(n.Mount.Mounter().Exec)
	if _, err := r.Resize(devicePath, volumePath); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not resize volume %q:  %v", volumeID, err)
	}

	klog.V(3).InfoS("NodeExpandVolume: resized volume", "device", devicePath, "path", volumePath)

	return &csi.NodeExpandVolumeResponse{}, nil
}

// NodeGetCapabilities get the node capabilities
func (n *NodeService) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(4).InfoS("NodeGetCapabilities: called")

	caps := []*csi.NodeServiceCapability{}

	for _, cap := range nodeCaps {
		c := &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: cap,
				},
			},
		}
		caps = append(caps, c)
	}

	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

// NodeGetInfo get the node info
func (n *NodeService) NodeGetInfo(ctx context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(4).InfoS("NodeGetInfo: called")

	node, err := n.kclient.CoreV1().Nodes().Get(ctx, n.nodeID, metav1.GetOptions{})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get node %s: %v", n.nodeID, err))
	}

	region, zone := GetNodeTopology(node.Labels)
	if region == "" || zone == "" {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get region or zone for node %s", n.nodeID))
	}

	return &csi.NodeGetInfoResponse{
		NodeId:            n.nodeID,
		MaxVolumesPerNode: maxVolumes(node),
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				corev1.LabelTopologyRegion: region,
				corev1.LabelTopologyZone:   zone,
			},
		},
	}, nil
}

func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) bool {
	hasSupport := func(reqcap *csi.VolumeCapability) bool {
		for _, c := range volumeCaps {
			if c == reqcap.GetAccessMode().GetMode() {
				return true
			}
		}

		return false
	}

	foundAll := true

	for _, c := range volCaps {
		if !hasSupport(c) {
			foundAll = false
		}
	}

	return foundAll
}

func collectMountOptions(params StorageParameters, fsType string, mntFlags []string) []string {
	options := []string{}
	options = append(options, mntFlags...)

	if params.SSD != nil && *params.SSD {
		options = append(options, "noatime")
	}

	// By default, xfs does not allow mounting of two volumes with the same filesystem uuid.
	// Force ignore this uuid to be able to mount volume + its clone / restored snapshot on the same node.
	if fsType == FSTypeXfs {
		options = append(options, "nouuid")
	}

	return options
}

func collectFormatOptions(params StorageParameters, fsType string) []string {
	formatOptions := []string{}

	if params.BlockSize != nil && *params.BlockSize > 0 {
		blockSize := fmt.Sprintf("%d", *params.BlockSize)

		if fsType == FSTypeXfs {
			blockSize = fmt.Sprintf("size=%d", *params.BlockSize)
		}

		formatOptions = append(formatOptions, "-b", blockSize)
	}

	if params.InodeSize != nil && *params.InodeSize > 0 {
		option, inodeSize := "-I", fmt.Sprintf("%d", *params.InodeSize)

		if fsType == FSTypeXfs {
			option, inodeSize = "-i", fmt.Sprintf("size=%d", *params.InodeSize)
		}

		formatOptions = append(formatOptions, option, inodeSize)
	}

	return formatOptions
}

func maxVolumes(node *corev1.Node) int64 {
	volumes, err := strconv.ParseInt(node.Labels[NodeLabelMaxVolumeAttachments], 10, 64)
	if err != nil {
		volumes = DefaultMaxVolumesPerNode
	}

	if volumes < 0 || volumes > VolumesPerNodeHardLimit {
		klog.V(3).InfoS("Node has out of range value for max volume attachments, using default",
			"node", node.Name,
			"label", NodeLabelMaxVolumeAttachments,
			"value", volumes,
			"hardLimit", VolumesPerNodeHardLimit,
			"default", DefaultMaxVolumesPerNode)

		return DefaultMaxVolumesPerNode
	}

	return volumes
}
