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

// Package proxmoxpool provides a pool of Telmate/proxmox-api-go/proxmox clients
package proxmoxpool

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	proxmox "github.com/luthermonson/go-proxmox"

	goproxmox "github.com/sergelogvinov/go-proxmox"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

// ProxmoxCluster defines a Proxmox cluster configuration.
type ProxmoxCluster struct {
	URL             string `yaml:"url"`
	Insecure        bool   `yaml:"insecure,omitempty"`
	TokenID         string `yaml:"token_id,omitempty"`
	TokenIDFile     string `yaml:"token_id_file,omitempty"`
	TokenSecret     string `yaml:"token_secret,omitempty"`
	TokenSecretFile string `yaml:"token_secret_file,omitempty"`
	Username        string `yaml:"username,omitempty"`
	Password        string `yaml:"password,omitempty"`
	Region          string `yaml:"region,omitempty"`
}

// ProxmoxPool is a Proxmox client pool of proxmox clusters.
type ProxmoxPool struct {
	clients map[string]*goproxmox.APIClient
}

// NewProxmoxPool creates a new Proxmox cluster client.
func NewProxmoxPool(config []*ProxmoxCluster, options ...proxmox.Option) (*ProxmoxPool, error) {
	clusters := len(config)
	if clusters > 0 {
		clients := make(map[string]*goproxmox.APIClient, clusters)

		for _, cfg := range config {
			opts := []proxmox.Option{proxmox.WithUserAgent("ProxmoxCSIPlugin/1.0")}
			opts = append(opts, options...)

			if cfg.Insecure {
				httpTr := &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true,
						MinVersion:         tls.VersionTLS12,
					},
				}

				opts = append(opts, proxmox.WithHTTPClient(&http.Client{Transport: httpTr}))
			}

			if cfg.TokenID == "" && cfg.TokenIDFile != "" {
				var err error

				cfg.TokenID, err = readValueFromFile(cfg.TokenIDFile)
				if err != nil {
					return nil, err
				}
			}

			if cfg.TokenSecret == "" && cfg.TokenSecretFile != "" {
				var err error

				cfg.TokenSecret, err = readValueFromFile(cfg.TokenSecretFile)
				if err != nil {
					return nil, err
				}
			}

			if cfg.Username != "" && cfg.Password != "" {
				opts = append(opts, proxmox.WithCredentials(&proxmox.Credentials{
					Username: cfg.Username,
					Password: cfg.Password,
				}))
			} else if cfg.TokenID != "" && cfg.TokenSecret != "" {
				opts = append(opts, proxmox.WithAPIToken(cfg.TokenID, cfg.TokenSecret))
			}

			pxClient, err := goproxmox.NewAPIClient(cfg.URL, opts...)
			if err != nil {
				return nil, err
			}

			clients[cfg.Region] = pxClient
		}

		return &ProxmoxPool{
			clients: clients,
		}, nil
	}

	return nil, ErrClustersNotFound
}

// GetRegions returns supported regions.
func (c *ProxmoxPool) GetRegions() []string {
	regions := make([]string, 0, len(c.clients))

	for region := range c.clients {
		regions = append(regions, region)
	}

	return regions
}

// CheckClusters checks if the Proxmox connection is working.
func (c *ProxmoxPool) CheckClusters(ctx context.Context) error {
	for region, pxClient := range c.clients {
		if _, err := pxClient.Version(ctx); err != nil {
			return fmt.Errorf("failed to initialized proxmox client in region %s, error: %v", region, err)
		}

		pxCluster, err := pxClient.Cluster(ctx)
		if err != nil {
			return fmt.Errorf("failed to get cluster info in region %s, error: %v", region, err)
		}

		// Check if we can have permission to list VMs
		vms, err := pxCluster.Resources(ctx, "vm")
		if err != nil {
			return fmt.Errorf("failed to get list of VMs in region %s, error: %v", region, err)
		}

		if len(vms) > 0 {
			klog.V(4).InfoS("Proxmox cluster has VMs", "region", region, "count", len(vms))
		} else {
			klog.InfoS("Proxmox cluster has no VMs, or check the account permission", "region", region)
		}
	}

	return nil
}

// GetProxmoxCluster returns a Proxmox cluster client in a given region.
func (c *ProxmoxPool) GetProxmoxCluster(region string) (*goproxmox.APIClient, error) {
	if c.clients[region] != nil {
		return c.clients[region], nil
	}

	return nil, ErrRegionNotFound
}

// GetVMByIDInRegion returns a Proxmox VM by its ID in a given region.
func (c *ProxmoxPool) GetVMByIDInRegion(ctx context.Context, region string, vmid uint64) (*proxmox.ClusterResource, error) {
	px, err := c.GetProxmoxCluster(region)
	if err != nil {
		return nil, err
	}

	vm, err := px.FindVMByID(ctx, uint64(vmid)) //nolint: unconvert
	if err != nil {
		return nil, err
	}

	return vm, nil
}

// DeleteVMByIDInRegion deletes a Proxmox VM by its ID in a given region.
func (c *ProxmoxPool) DeleteVMByIDInRegion(ctx context.Context, region string, vm *proxmox.ClusterResource) error {
	px, err := c.GetProxmoxCluster(region)
	if err != nil {
		return err
	}

	return px.DeleteVMByID(ctx, vm.Node, int(vm.VMID))
}

// GetNodeGroup returns a Proxmox node ha-group in a given region.
func (c *ProxmoxPool) GetNodeGroup(ctx context.Context, region string, node string) (string, error) {
	px, err := c.GetProxmoxCluster(region)
	if err != nil {
		return "", err
	}

	haGroups, err := px.GetHAGroupList(ctx)
	if err != nil {
		return "", fmt.Errorf("error get ha-groups %v", err)
	}

	for _, g := range haGroups {
		if g.Type != "group" {
			continue
		}

		for _, n := range strings.Split(g.Nodes, ",") {
			if node == strings.Split(n, ":")[0] {
				return g.Group, nil
			}
		}
	}

	return "", ErrHAGroupNotFound
}

// FindVMByNode find a VM by kubernetes node resource in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByNode(ctx context.Context, node *v1.Node) (vmID int, region string, err error) {
	for region, px := range c.clients {
		vmid, err := px.FindVMByFilter(ctx, func(rs *proxmox.ClusterResource) (bool, error) {
			if rs.Type != "qemu" {
				return false, nil
			}

			if !strings.HasPrefix(rs.Name, node.Name) {
				return false, nil
			}

			pxnode, err := px.Client.Node(ctx, rs.Node)
			if err != nil {
				return false, err
			}

			vm, err := pxnode.VirtualMachine(ctx, int(rs.VMID))
			if err != nil {
				return false, err
			}

			smbios1 := goproxmox.VMSMBIOS{}
			smbios1.UnmarshalString(vm.VirtualMachineConfig.SMBios1) //nolint:errcheck

			if smbios1.UUID == node.Status.NodeInfo.SystemUUID {
				return true, nil
			}

			return false, nil
		})
		if err != nil {
			if err == goproxmox.ErrVirtualMachineNotFound {
				continue
			}

			return 0, "", err
		}

		if vmid == 0 {
			continue
		}

		return vmid, region, nil
	}

	return 0, "", ErrInstanceNotFound
}

// FindVMByUUID find a VM by uuid in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByUUID(ctx context.Context, uuid string) (vmID int, region string, err error) {
	for region, px := range c.clients {
		vmid, err := px.FindVMByFilter(ctx, func(rs *proxmox.ClusterResource) (bool, error) {
			if rs.Type != "qemu" {
				return false, nil
			}

			pxnode, err := px.Client.Node(ctx, rs.Node)
			if err != nil {
				return false, err
			}

			vm, err := pxnode.VirtualMachine(ctx, int(rs.VMID))
			if err != nil {
				return false, err
			}

			if c.GetVMUUID(vm) == uuid {
				return true, nil
			}

			return false, nil
		})
		if err != nil {
			if errors.Is(err, goproxmox.ErrVirtualMachineNotFound) {
				continue
			}

			return 0, "", ErrInstanceNotFound
		}

		return vmid, region, nil
	}

	return 0, "", ErrInstanceNotFound
}

// GetVMUUID returns the VM UUID.
func (c *ProxmoxPool) GetVMUUID(vm *proxmox.VirtualMachine) string {
	smbios1 := goproxmox.VMSMBIOS{}
	smbios1.UnmarshalString(vm.VirtualMachineConfig.SMBios1) //nolint:errcheck

	return smbios1.UUID
}

// GetVMSKU returns the VM instance type name.
func (c *ProxmoxPool) GetVMSKU(vm *proxmox.VirtualMachine) string {
	smbios1 := goproxmox.VMSMBIOS{}
	smbios1.UnmarshalString(vm.VirtualMachineConfig.SMBios1) //nolint:errcheck

	sku, _ := base64.StdEncoding.DecodeString(smbios1.SKU) //nolint:errcheck

	return string(sku)
}

func readValueFromFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file '%s': %w", path, err)
	}

	return strings.TrimSpace(string(content)), nil
}
