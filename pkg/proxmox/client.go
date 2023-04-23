// Package cloud implements the multi-cloud provider interface for Proxmox.
package cloud

import (
	"crypto/tls"
	"os"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

	"k8s.io/klog/v2"
)

// Client is a Proxmox client.
type Client struct {
	config  *ClustersConfig
	proxmox map[string]*pxapi.Client
}

// NewClient creates a new Proxmox client.
func NewClient(config *ClustersConfig) (*Client, error) {
	clusters := len(config.Clusters)
	if clusters > 0 {
		proxmox := make(map[string]*pxapi.Client, clusters)

		for _, cfg := range config.Clusters {
			tlsconf := &tls.Config{InsecureSkipVerify: true}
			if !cfg.Insecure {
				tlsconf = nil
			}

			client, err := pxapi.NewClient(cfg.URL, nil, os.Getenv("PM_HTTP_HEADERS"), tlsconf, "", 600)
			if err != nil {
				return nil, err
			}

			client.SetAPIToken(cfg.TokenID, cfg.TokenSecret)

			if _, err := client.GetVersion(); err != nil {
				klog.Errorf("failed to initialized proxmox client in cluster %s: %v", cfg.Region, err)

				return nil, err
			}

			proxmox[cfg.Region] = client
		}

		return &Client{
			config:  config,
			proxmox: proxmox,
		}, nil
	}

	return nil, nil
}

// GetProxmoxCluster returns a Proxmox cluster client in a given region.
func (c *Client) GetProxmoxCluster(region string) (*pxapi.Client, error) {
	if c.proxmox[region] != nil {
		return c.proxmox[region], nil
	}

	return nil, nil
}
