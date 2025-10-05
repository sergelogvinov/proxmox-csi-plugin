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
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

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

// ProxmoxPool is a Proxmox client.
type ProxmoxPool struct {
	clients map[string]*pxapi.Client
}

// NewProxmoxPool creates a new Proxmox cluster client.
func NewProxmoxPool(config []*ProxmoxCluster, hClient *http.Client) (*ProxmoxPool, error) {
	clusters := len(config)
	if clusters > 0 {
		proxmox := make(map[string]*pxapi.Client, clusters)

		for _, cfg := range config {
			tlsconf := &tls.Config{InsecureSkipVerify: true}
			if !cfg.Insecure {
				tlsconf = nil
			}

			pClient, err := pxapi.NewClient(cfg.URL, hClient, os.Getenv("PM_HTTP_HEADERS"), tlsconf, "", 600)
			if err != nil {
				return nil, err
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
				if err := pClient.Login(cfg.Username, cfg.Password, ""); err != nil {
					return nil, err
				}
			} else {
				pClient.SetAPIToken(cfg.TokenID, cfg.TokenSecret)
			}

			proxmox[cfg.Region] = pClient
		}

		return &ProxmoxPool{
			clients: proxmox,
		}, nil
	}

	return nil, fmt.Errorf("no Proxmox clusters found")
}

// CheckClusters checks if the Proxmox connection is working.
func (c *ProxmoxPool) CheckClusters(_ context.Context) error {
	for region, pClient := range c.clients {
		if _, err := pClient.GetVersion(); err != nil {
			return fmt.Errorf("failed to initialized proxmox client in region %s, error: %v", region, err)
		}

		vmlist, err := pClient.GetVmList()
		if err != nil {
			return fmt.Errorf("failed to get list of VMs in region %s, error: %v", region, err)
		}

		vms, ok := vmlist["data"].([]interface{})
		if !ok {
			return fmt.Errorf("failed to cast response to list of VMs in region %s, error: %v", region, err)
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
func (c *ProxmoxPool) GetProxmoxCluster(region string) (*pxapi.Client, error) {
	if c.clients[region] != nil {
		return c.clients[region], nil
	}

	return nil, fmt.Errorf("proxmox cluster %s not found", region)
}

// FindVMByNode find a VM by kubernetes node resource in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByNode(_ context.Context, node *v1.Node) (*pxapi.VmRef, string, error) {
	for region, px := range c.clients {
		vmrs, err := px.GetVmRefsByName(node.Name)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				continue
			}

			return nil, "", err
		}

		for _, vmr := range vmrs {
			config, err := px.GetVmConfig(vmr)
			if err != nil {
				return nil, "", err
			}

			if c.GetVMUUID(config) == node.Status.NodeInfo.SystemUUID {
				return vmr, region, nil
			}
		}
	}

	return nil, "", fmt.Errorf("vm '%s' not found", node.Name)
}

// FindVMByName find a VM by name in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByName(_ context.Context, name string) (*pxapi.VmRef, string, error) {
	for region, px := range c.clients {
		vmr, err := px.GetVmRefByName(name)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				continue
			}

			return nil, "", err
		}

		return vmr, region, nil
	}

	return nil, "", fmt.Errorf("vm '%s' not found", name)
}

// FindVMByUUID find a VM by uuid in all Proxmox clusters.
func (c *ProxmoxPool) FindVMByUUID(_ context.Context, uuid string) (*pxapi.VmRef, string, error) {
	for region, px := range c.clients {
		vms, err := px.GetResourceList("vm")
		if err != nil {
			return nil, "", fmt.Errorf("error get resources %v", err)
		}

		for vmii := range vms {
			vm, ok := vms[vmii].(map[string]interface{})
			if !ok {
				return nil, "", fmt.Errorf("failed to cast response to map, vm: %v", vm)
			}

			if vm["type"].(string) != "qemu" { //nolint:errcheck
				continue
			}

			vmr := pxapi.NewVmRef(int(vm["vmid"].(float64))) //nolint:errcheck
			vmr.SetNode(vm["node"].(string))                 //nolint:errcheck
			vmr.SetVmType("qemu")

			config, err := px.GetVmConfig(vmr)
			if err != nil {
				return nil, "", err
			}

			if config["smbios1"] != nil {
				if c.getSMBSetting(config, "uuid") == uuid {
					return vmr, region, nil
				}
			}
		}
	}

	return nil, "", fmt.Errorf("vm with uuid '%s' not found", uuid)
}

// GetVMName returns the VM name.
func (c *ProxmoxPool) GetVMName(vmInfo map[string]interface{}) string {
	if vmInfo["name"] != nil {
		return vmInfo["name"].(string) //nolint:errcheck
	}

	return ""
}

// GetVMUUID returns the VM UUID.
func (c *ProxmoxPool) GetVMUUID(vmInfo map[string]interface{}) string {
	if vmInfo["smbios1"] != nil {
		return c.getSMBSetting(vmInfo, "uuid")
	}

	return ""
}

// GetVMSKU returns the VM instance type name.
func (c *ProxmoxPool) GetVMSKU(vmInfo map[string]interface{}) string {
	if vmInfo["smbios1"] != nil {
		return c.getSMBSetting(vmInfo, "sku")
	}

	return ""
}

func (c *ProxmoxPool) getSMBSetting(vmInfo map[string]interface{}, name string) string {
	smbios, ok := vmInfo["smbios1"].(string)
	if !ok {
		return ""
	}

	for _, l := range strings.Split(smbios, ",") {
		if l == "" || l == "base64=1" {
			continue
		}

		parsedParameter, err := url.ParseQuery(l)
		if err != nil {
			return ""
		}

		for k, v := range parsedParameter {
			if k == name {
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
