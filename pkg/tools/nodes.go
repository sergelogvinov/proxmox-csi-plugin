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
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

	proxmox "github.com/sergelogvinov/proxmox-cloud-controller-manager/pkg/cluster"
	"github.com/sergelogvinov/proxmox-cloud-controller-manager/pkg/provider"

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

// ProxmoxVMID returns the Proxmox VM ID from the specified kubernetes node name.
func ProxmoxVMID(ctx context.Context, kclient clientkubernetes.Interface, px *pxapi.Client, nodeName string, prov proxmox.Provider) (int, string, error) {
	node, err := kclient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return 0, "", fmt.Errorf("failed to get node: %v", err)
	}

	if prov == proxmox.ProviderCapmox {
		return ProxmoxVMIDByProviderID(px, node.Spec.ProviderID)
	}

	vmID, err := provider.GetVMID(node.Spec.ProviderID)

	return vmID, node.Labels[corev1.LabelTopologyZone], err
}

// ProxmoxVMIDByProviderID find a VM by uuid in all Proxmox clusters.
func ProxmoxVMIDByProviderID(px *pxapi.Client, providerID string) (int, string, error) {
	uuid := strings.TrimPrefix(providerID, "proxmox://")

	vm, err := findVMByUUID(px, uuid)
	if err != nil {
		return 0, "", err
	}

	return vm.VmId(), vm.Node(), nil
}

func findVMByUUID(px *pxapi.Client, uuid string) (*pxapi.VmRef, error) {
	vms, err := px.GetResourceList("vm")
	if err != nil {
		return nil, fmt.Errorf("error get resources %v", err)
	}

	for vmii := range vms {
		vm, ok := vms[vmii].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("failed to cast response to map, vm: %v", vm)
		}

		if vm["type"].(string) != "qemu" { //nolint:errcheck
			continue
		}

		vmr := pxapi.NewVmRef(int(vm["vmid"].(float64))) //nolint:errcheck
		vmr.SetNode(vm["node"].(string))                 //nolint:errcheck
		vmr.SetVmType("qemu")

		config, err := px.GetVmConfig(vmr)
		if err != nil {
			return nil, err
		}

		if config["smbios1"] != nil {
			if getUUID(config["smbios1"].(string)) == uuid { //nolint:errcheck
				return vmr, nil
			}
		}
	}

	return nil, fmt.Errorf("vm with uuid '%s' not found", uuid)
}

func getUUID(smbios string) string {
	for _, l := range strings.Split(smbios, ",") {
		if l == "" || l == "base64=1" {
			continue
		}

		parsedParameter, err := url.ParseQuery(l)
		if err != nil {
			return ""
		}

		for k, v := range parsedParameter {
			if k == "uuid" {
				decodedString, err := base64.StdEncoding.DecodeString(v[0])
				if err != nil {
					decodedString = []byte(v[0])
				}

				return string(decodedString)
			}
		}
	}

	return ""
}
