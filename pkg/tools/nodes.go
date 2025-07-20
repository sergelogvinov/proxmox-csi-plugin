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

package tools

import (
	"context"
	"fmt"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/provider"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

// CSINodes returns a list of nodes that have the specified CSI driver name.
func CSINodes(ctx context.Context, kclient *clientkubernetes.Clientset, csiDriverName string) ([]string, error) {
	nodes := []string{}

	csinodes, err := kclient.StorageV1().CSINodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list CSINodes: %v", err)
	}

	for _, csinode := range csinodes.Items {
		for _, driver := range csinode.Spec.Drivers {
			if driver.Name == csiDriverName {
				nodes = append(nodes, driver.NodeID)

				break
			}
		}
	}

	return nodes, nil
}

// CondonNodes condones the specified nodes.
func CondonNodes(ctx context.Context, kclient *clientkubernetes.Clientset, nodes []string) ([]string, error) {
	cordonedNodes := []string{}
	patch := []byte(`{"spec":{"unschedulable":true}}`)

	for _, node := range nodes {
		nodeStatus, err := kclient.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get node status: %v", err)
		}

		if !nodeStatus.Spec.Unschedulable {
			_, err = kclient.CoreV1().Nodes().Patch(ctx, node, types.MergePatchType, patch, metav1.PatchOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to cordon node: %v", err)
			}

			cordonedNodes = append(cordonedNodes, node)
		}
	}

	return cordonedNodes, nil
}

// UncondonNodes uncondones the specified nodes.
func UncondonNodes(ctx context.Context, kclient *clientkubernetes.Clientset, nodes []string) error {
	patch := []byte(`{"spec":{"unschedulable":false}}`)

	for _, node := range nodes {
		_, err := kclient.CoreV1().Nodes().Patch(ctx, node, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("failed to uncordon node: %v", err)
		}
	}

	return nil
}

// ProxmoxVMIDbyProviderID returns the Proxmox VM ID from the specified kubernetes node name.
func ProxmoxVMIDbyProviderID(_ context.Context, node *corev1.Node) (int, string, error) {
	vmID, err := provider.GetVMID(node.Spec.ProviderID)

	return vmID, node.Labels[corev1.LabelTopologyZone], err
}
