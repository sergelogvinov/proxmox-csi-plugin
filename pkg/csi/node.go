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
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

var (
	volumeCaps = []csi.VolumeCapability_AccessMode{
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
)

type nodeService struct {
	nodeID  string
	kclient clientkubernetes.Interface
}

func newNodeService(nodeID string) (nodeService, error) {
	// config, err := rest.InClusterConfig()
	config, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		return nodeService{}, err
	}

	return nodeService{
		nodeID:  nodeID,
		kclient: clientkubernetes.NewForConfigOrDie(config),
	}, nil
}

// NodeStageVolume is called by the CO when a workload that wants to use the specified volume is placed (scheduled) on a node.
func (n *nodeService) NodeStageVolume(ctx context.Context, request *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.V(4).Infof("NodeStageVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// NodeUnstageVolume is called by the CO when a workload that was using the specified volume is being moved to a different node.
func (n *nodeService) NodeUnstageVolume(ctx context.Context, request *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(4).Infof("NodeUnstageVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// NodePublishVolume mounts the volume on the node.
func (n *nodeService) NodePublishVolume(ctx context.Context, request *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.V(4).Infof("NodePublishVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume id not provided")
	}

	target := request.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	volCap := request.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}

	if !isValidVolumeCapabilities([]*csi.VolumeCapability{volCap}) {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not supported")
	}

	// readOnly := false
	// if request.GetReadonly() || request.VolumeCapability.AccessMode.GetMode() == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {
	// 	readOnly = true
	// }

	options := make(map[string]string)
	if m := volCap.GetMount(); m != nil {
		for _, f := range m.MountFlags {
			// get mountOptions from PV.spec.mountOptions
			options[f] = ""
		}
	}

	// TODO modify your volume mount logic here

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume from the target path
func (n *nodeService) NodeUnpublishVolume(ctx context.Context, request *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(4).Infof("NodeUnpublishVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	target := request.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	// TODO modify your volume umount logic here

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats get the volume stats
func (n *nodeService) NodeGetVolumeStats(ctx context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(4).Infof("NodeGetVolumeStats: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// NodeExpandVolume expand the volume
func (n *nodeService) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	klog.V(4).Infof("NodeExpandVolume: called with args %+v", protosanitizer.StripSecrets(*request))

	return nil, status.Error(codes.Unimplemented, "")
}

// NodeGetCapabilities get the node capabilities
func (n *nodeService) NodeGetCapabilities(ctx context.Context, request *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(4).Infof("NodeGetCapabilities: called with args %+v", protosanitizer.StripSecrets(*request))

	return &csi.NodeGetCapabilitiesResponse{}, nil
}

// NodeGetInfo get the node info
func (n *nodeService) NodeGetInfo(ctx context.Context, request *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(4).Infof("NodeGetInfo: called with args %+v", protosanitizer.StripSecrets(*request))

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

	return &csi.NodeGetInfoResponse{
		NodeId:            n.nodeID,
		MaxVolumesPerNode: 10,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				corev1.LabelTopologyRegion: region,
				corev1.LabelTopologyZone:   zone,
			},
		},
	}, nil
}

func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) bool {
	hasSupport := func(cap *csi.VolumeCapability) bool {
		for _, c := range volumeCaps {
			if c.GetMode() == cap.AccessMode.GetMode() {
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
