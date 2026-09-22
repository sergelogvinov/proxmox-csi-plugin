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

// Package proxmoxpool provides a pool of github.com/sergelogvinov/go-proxmox-rest
// clients, one per configured Proxmox cluster.
package proxmoxpool

import (
	"context"
	"fmt"
	"os"
	"strings"

	proxmoxrest "github.com/sergelogvinov/go-proxmox-rest"
	"github.com/sergelogvinov/go-proxmox-rest/cluster"

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
	// clientsRest are the go-proxmox-rest clients, one per configured cluster.
	clientsRest map[string]*proxmoxrest.Client
}

// NewProxmoxPool creates a new Proxmox cluster client.
func NewProxmoxPool(config []*ProxmoxCluster, options ...proxmoxrest.Option) (*ProxmoxPool, error) {
	clusters := len(config)
	if clusters > 0 {
		clientsRest := make(map[string]*proxmoxrest.Client, clusters)

		for _, cfg := range config {
			restOpts := []proxmoxrest.Option{
				proxmoxrest.WithURL(cfg.URL),
				proxmoxrest.WithInsecure(cfg.Insecure),
				proxmoxrest.WithUserAgent("ProxmoxCSIPlugin/1.0"),
			}
			restOpts = append(restOpts, options...)

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
				restOpts = append(restOpts, proxmoxrest.WithPasswordAuth(cfg.Username, cfg.Password))
			} else if cfg.TokenID != "" && cfg.TokenSecret != "" {
				restOpts = append(restOpts, proxmoxrest.WithTokenAuth(cfg.TokenID, cfg.TokenSecret))
			}

			pxClientRest, err := proxmoxrest.New(proxmoxrest.ClientConfig{}, restOpts...)
			if err != nil {
				return nil, fmt.Errorf("failed to create Proxmox REST client for region %s: %w", cfg.Region, err)
			}

			clientsRest[cfg.Region] = pxClientRest
		}

		return &ProxmoxPool{
			clientsRest: clientsRest,
		}, nil
	}

	return nil, ErrClustersNotFound
}

// GetRegions returns supported regions.
func (c *ProxmoxPool) GetRegions() []string {
	regions := make([]string, 0, len(c.clientsRest))

	for region := range c.clientsRest {
		regions = append(regions, region)
	}

	return regions
}

// CheckClusters checks if the Proxmox connection is working.
func (c *ProxmoxPool) CheckClusters(ctx context.Context) error {
	for region, pxClient := range c.clientsRest {
		info, err := pxClient.Version(ctx)
		if err != nil {
			return fmt.Errorf("failed to initialized proxmox client in region %s, error: %v", region, err)
		}

		// Check if we can have permission to list VMs
		vms, err := pxClient.Cluster().Resources().List(ctx, cluster.ListFilter{Type: cluster.ResourceTypeVM})
		if err != nil {
			return fmt.Errorf("failed to get list of VMs in region %s, error: %v", region, err)
		}

		if len(vms) > 0 {
			klog.V(4).InfoS("Proxmox cluster information", "region", region, "version", info.Version, "vms", len(vms))
		} else {
			klog.InfoS("Proxmox cluster has no VMs, or check the account permission", "region", region)
		}
	}

	return nil
}

// GetProxmoxCluster returns a Proxmox REST API client
// (github.com/sergelogvinov/go-proxmox-rest) in a given region.
func (c *ProxmoxPool) GetProxmoxCluster(region string) (*proxmoxrest.Client, error) {
	if c.clientsRest[region] != nil {
		return c.clientsRest[region], nil
	}

	return nil, ErrRegionNotFound
}

// SetProxmoxCluster overrides the REST client for a region. Intended for tests
// that need to point a region at an in-memory fake server after the pool has
// already been built from static configuration (e.g. a YAML file with a fixed URL).
func (c *ProxmoxPool) SetProxmoxCluster(region string, client *proxmoxrest.Client) {
	c.clientsRest[region] = client
}

// GetNodeGroup returns a Proxmox node ha-group in a given region.
func (c *ProxmoxPool) GetNodeGroup(ctx context.Context, region string, node string) (string, error) {
	px, err := c.GetProxmoxCluster(region)
	if err != nil {
		return "", err
	}

	haGroups, err := px.Cluster().HA().Groups().List(ctx)
	if err != nil {
		return "", fmt.Errorf("error get ha-groups %v", err)
	}

	for _, g := range haGroups {
		if g.Type != "group" {
			continue
		}

		for n := range strings.SplitSeq(g.Nodes, ",") {
			if node == strings.Split(n, ":")[0] {
				return g.Group, nil
			}
		}
	}

	return "", ErrHAGroupNotFound
}

// FindVMByNode find a VM by kubernetes node resource in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByNode(ctx context.Context, node *v1.Node) (vmID int, region string, err error) {
	for region, px := range c.clientsRest {
		resources, err := px.Cluster().Resources().List(ctx, cluster.ListFilter{
			Type:      cluster.ResourceTypeVM,
			GuestType: "qemu",
			Match: func(rs *cluster.Resource) (bool, error) {
				if !strings.HasPrefix(rs.Name, node.Name) {
					return false, nil
				}

				cfg, err := px.Nodes(rs.Node).Qemu().Config(ctx, rs.VMID, nil)
				if err != nil {
					return false, err
				}

				return cfg.SMBios1 != nil && cfg.SMBios1.UUID == node.Status.NodeInfo.SystemUUID, nil
			},
		})
		if err != nil {
			return 0, "", err
		}

		if len(resources) == 0 {
			continue
		}

		return resources[0].VMID, region, nil
	}

	return 0, "", ErrInstanceNotFound
}

// FindVMByUUID find a VM by uuid in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByUUID(ctx context.Context, uuid string) (vmID int, region string, err error) {
	for region, px := range c.clientsRest {
		resources, err := px.Cluster().Resources().List(ctx, cluster.ListFilter{
			Type:      cluster.ResourceTypeVM,
			GuestType: "qemu",
			Match: func(rs *cluster.Resource) (bool, error) {
				cfg, err := px.Nodes(rs.Node).Qemu().Config(ctx, rs.VMID, nil)
				if err != nil {
					return false, err
				}

				return cfg.SMBios1 != nil && cfg.SMBios1.UUID == uuid, nil
			},
		})
		if err != nil {
			return 0, "", err
		}

		if len(resources) == 0 {
			continue
		}

		return resources[0].VMID, region, nil
	}

	return 0, "", ErrInstanceNotFound
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
