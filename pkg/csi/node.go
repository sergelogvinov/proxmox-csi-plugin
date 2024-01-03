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
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
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

var volumeCaps = []csi.VolumeCapability_AccessMode{
	{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	},
	{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
	},
	{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
	},
	{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
	},
}

// NodeService is the node service for the CSI driver
type NodeService struct {
	nodeID  string
	kclient kubernetes.Interface

	Mount mount.IMount
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
	klog.V(4).Infof("NodeStageVolume: called with args %s", stripSecrets(*request))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeID must be provided")
	}

	stagingTarget := request.GetStagingTargetPath()
	if len(stagingTarget) == 0 {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath must be provided")
	}

	volumeCapability := request.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability must be provided")
	}

	volumeContext := request.GetVolumeContext()
	if volumeContext == nil {
		volumeContext = map[string]string{}
	}

	devicePath := request.GetPublishContext()["DevicePath"]
	if len(devicePath) == 0 {
		klog.Errorf("NodePublishVolume: DevicePath must be provided")

		return nil, status.Error(codes.InvalidArgument, "DevicePath must be provided")
	}

	m := n.Mount

	if blk := volumeCapability.GetBlock(); blk != nil {
		return &csi.NodeStageVolumeResponse{}, nil
	}

	notMnt, err := m.IsLikelyNotMountPointAttach(stagingTarget)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if notMnt {
		var (
			options       []string
			formatOptions []string
		)

		fsType := FSTypeExt4

		if mnt := volumeCapability.GetMount(); mnt != nil {
			if mnt.FsType != "" {
				fsType = mnt.FsType
			}

			if volumeContext["ssd"] == "true" {
				options = append(options, "noatime")
			}

			mountFlags := mnt.GetMountFlags()
			options = append(options, collectMountOptions(fsType, mountFlags)...)
		}

		blockSize := volumeContext[StorageBlockSizeKey]
		if blockSize != "" {
			if fsType == FSTypeXfs {
				blockSize = "size=" + blockSize
			}

			formatOptions = append(formatOptions, "-b", blockSize)
		}

		inodeSize := volumeContext[StorageInodeSizeKey]
		if inodeSize != "" {
			option := "-I"
			if fsType == FSTypeXfs {
				option, inodeSize = "-i", "size="+inodeSize
			}

			formatOptions = append(formatOptions, option, inodeSize)
		}

		passphraseKey, ok := request.GetSecrets()[EncryptionPassphraseKey]
		if ok {
			klog.V(4).Infof("NodeStageVolume: volume encrypted")

			sb, err := filesystem.Probe(devicePath) //nolint:govet
			if err != nil {
				klog.Errorf("NodeStageVolume: failed to probe filesystem for device %s, error: %v", devicePath, err)
			}

			key := encryption.NewKey(encryption.AnyKeyslot, []byte(passphraseKey))
			l := luks.New(luks.AESXTSPlain64Cipher)

			if sb == nil {
				if err = l.Encrypt(devicePath, key); err != nil {
					klog.Errorf("NodeStageVolume: failed to encrypt device %s, error: %v", devicePath, err)

					return nil, status.Error(codes.Internal, err.Error())
				}
			}

			lukskDevicePath, err := l.Open(devicePath, key) //nolint:govet
			if err != nil {
				klog.Errorf("NodeStageVolume: failed to open encrypted device %s, error: %v", devicePath, err)

				return nil, status.Error(codes.Internal, err.Error())
			}

			devicePath = lukskDevicePath
		}

		err = m.Mounter().FormatAndMountSensitiveWithFormatOptions(devicePath, stagingTarget, fsType, options, nil, formatOptions)
		if err != nil {
			klog.Errorf("NodeStageVolume: failed to mount device %s at %s (fstype: %s), error: %v", devicePath, stagingTarget, fsType, err)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume is called by the CO when a workload that was using the specified volume is being moved to a different node.
//
//nolint:dupl
func (n *NodeService) NodeUnstageVolume(_ context.Context, request *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(4).Infof("NodeUnstageVolume: called with args %s", stripSecrets(*request))

	stagingTargetPath := request.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath must be provided")
	}

	// Raw Block device is not mounted, so we can return here
	// https://github.com/kubernetes/kubernetes/blob/master/pkg/volume/csi/csi_block.go
	if strings.Contains(stagingTargetPath, "/kubernetes.io/csi/volumeDevices/") {
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	cmd := exec.New().Command("fstrim", "-v", stagingTargetPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		klog.Errorf("NodeUnstageVolume: failed to trim filesystem %s: %v\n", stagingTargetPath, err)
	} else {
		klog.V(4).Infof("NodeUnstageVolume: fstrim: %s\n", string(out))
	}

	sourcePath, err := n.Mount.GetMountFs(stagingTargetPath)
	if err != nil {
		klog.Errorf("NodeUnstageVolume: failed to find mount file system %s: %v", stagingTargetPath, err)
	}

	if err := n.Mount.UnmountPath(stagingTargetPath); err != nil {
		klog.Errorf("NodeUnstageVolume: failed to unmount targetPath %s, error: %v", stagingTargetPath, err)

		return nil, status.Errorf(codes.Internal, "Unmount of targetPath %s failed with error %v", stagingTargetPath, err)
	}

	devicePath := strings.TrimSpace(string(sourcePath))
	if strings.HasPrefix(devicePath, "/dev/mapper/") {
		l := luks.New(luks.AESXTSPlain64Cipher)
		if err := l.Close(devicePath); err != nil {
			klog.Errorf("NodeUnstageVolume: failed to close encrypted device %s, error: %v", devicePath, err)

			return nil, status.Errorf(codes.Internal, "Close encrypted device %s failed with error %v", devicePath, err)
		}
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume mounts the volume on the node.
//
//nolint:dupl
func (n *NodeService) NodePublishVolume(_ context.Context, request *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(4).Infof("NodePublishVolume: called with args %s", stripSecrets(*request))

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
		klog.Errorf("NodePublishVolume: VolumeCapability not supported")

		return nil, status.Error(codes.InvalidArgument, "VolumeCapability not supported")
	}

	devicePath := request.GetPublishContext()["DevicePath"]
	if len(devicePath) == 0 {
		klog.Errorf("NodePublishVolume: DevicePath must be provided")

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
		klog.Errorf("NodePublishVolume: stage volume is not mounted %s: %v", stagingTargetPath, err)

		return nil, status.Error(codes.NotFound, fmt.Sprintf("Failed to find mount file system %s: %v", stagingTargetPath, err))
	}

	notMnt, err := m.IsLikelyNotMountPointAttach(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if notMnt {
		fsType := "ext4"

		if mnt := volumeCapability.GetMount(); mnt != nil {
			if mnt.FsType != "" {
				fsType = mnt.FsType
			}
		}

		err = m.Mounter().Mount(stagingTargetPath, targetPath, fsType, mountOptions)
		if err != nil {
			klog.Errorf("NodePublishVolume: error mounting volume %s to %s: %v", stagingTargetPath, targetPath, err)

			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume from the target path
//
//nolint:dupl
func (n *NodeService) NodeUnpublishVolume(_ context.Context, request *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(4).Infof("NodeUnpublishVolume: called with args %s", stripSecrets(*request))

	targetPath := request.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "TargetPath must be provided")
	}

	err := n.Mount.UnmountPath(targetPath)
	if err != nil {
		klog.Errorf("Unmount of targetpath %s failed with error %v", targetPath, err)

		return nil, status.Errorf(codes.Internal, "Unmount of targetpath %s failed with error %v", targetPath, err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats get the volume stats
func (n *NodeService) NodeGetVolumeStats(_ context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(4).Infof("NodeGetVolumeStats: called with args %s", stripSecrets(*request))

	volumePath := request.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumePath must be provided")
	}

	exists, err := utilpath.Exists(utilpath.CheckFollowSymlink, request.VolumePath)
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

	klog.V(4).Infof("NodeGetVolumeStats: returning stats %+v", stats)

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{Total: stats.TotalBytes, Available: stats.AvailableBytes, Used: stats.UsedBytes, Unit: csi.VolumeUsage_BYTES},
			{Total: stats.TotalInodes, Available: stats.AvailableInodes, Used: stats.UsedInodes, Unit: csi.VolumeUsage_INODES},
		},
	}, nil
}

// NodeExpandVolume expand the volume
func (n *NodeService) NodeExpandVolume(_ context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	klog.V(4).Infof("NodeExpandVolume: called with args %s", stripSecrets(*request))

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
			klog.Errorf("NodeExpandVolume: failed to resize encrypted volume %s, ckeck feature gate CSINodeExpandSecret", devicePath)

			return nil, status.Errorf(codes.InvalidArgument, "Could not resize encrypted volume %s passphrase key is empty", devicePath)
		}

		key := encryption.NewKey(encryption.AnyKeyslot, []byte(passphraseKey))
		l := luks.New(luks.AESXTSPlain64Cipher)

		if err := l.Resize(devicePath, key); err != nil {
			klog.Errorf("NodeExpandVolume: failed to resize encrypted volume %s, error: %v", devicePath, err)

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

	return &csi.NodeExpandVolumeResponse{}, nil
}

// NodeGetCapabilities get the node capabilities
func (n *NodeService) NodeGetCapabilities(_ context.Context, request *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(4).Infof("NodeGetCapabilities: called with args %s", stripSecrets(*request))

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
func (n *NodeService) NodeGetInfo(ctx context.Context, request *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(4).Infof("NodeGetInfo: called with args %s", stripSecrets(*request))

	node, err := n.kclient.CoreV1().Nodes().Get(ctx, n.nodeID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node %s: %w", n.nodeID, err)
	}

	region := node.Labels[corev1.LabelTopologyRegion]
	if region == "" {
		return nil, fmt.Errorf("failed to get region for node %s", n.nodeID)
	}

	zone := node.Labels[corev1.LabelTopologyZone]
	if zone == "" {
		return nil, fmt.Errorf("failed to get zone for node %s", n.nodeID)
	}

	nodeID := n.nodeID

	// nodeID := node.Spec.ProviderID
	// if nodeID == "" {
	// 	nodeID = n.nodeID
	// }

	return &csi.NodeGetInfoResponse{
		NodeId:            nodeID,
		MaxVolumesPerNode: MaxVolumesPerNode,
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
			if c.GetMode() == reqcap.AccessMode.GetMode() {
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

func collectMountOptions(fsType string, mntFlags []string) []string {
	var options []string
	options = append(options, mntFlags...)

	// By default, xfs does not allow mounting of two volumes with the same filesystem uuid.
	// Force ignore this uuid to be able to mount volume + its clone / restored snapshot on the same node.
	if fsType == FSTypeXfs {
		options = append(options, "nouuid")
	}

	return options
}
