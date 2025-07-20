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

// Package config is the configuration for the cloud provider.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	yaml "gopkg.in/yaml.v3"

	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
)

// Provider specifies the provider. Can be 'default' or 'capmox'
type Provider string

// ProviderDefault is the default provider
const ProviderDefault Provider = "default"

// ProviderCapmox is the Provider for capmox
const ProviderCapmox Provider = "capmox"

// ClustersConfig is proxmox multi-cluster cloud config.
type ClustersConfig struct {
	Features struct {
		Provider Provider `yaml:"provider,omitempty"`
	} `yaml:"features,omitempty"`
	Clusters []*pxpool.ProxmoxCluster `yaml:"clusters,omitempty"`
}

// ReadCloudConfig reads cloud config from a reader.
func ReadCloudConfig(config io.Reader) (ClustersConfig, error) {
	cfg := ClustersConfig{}

	if config != nil {
		if err := yaml.NewDecoder(config).Decode(&cfg); err != nil {
			return ClustersConfig{}, err
		}
	}

	for idx, c := range cfg.Clusters {
		if c.Username != "" && c.Password != "" {
			if c.TokenID != "" || c.TokenSecret != "" {
				return ClustersConfig{}, fmt.Errorf("cluster #%d: token_id and token_secret are not allowed when username and password are set", idx+1)
			}
		} else if c.TokenID == "" || c.TokenSecret == "" {
			return ClustersConfig{}, fmt.Errorf("cluster #%d: either username and password or token_id and token_secret are required", idx+1)
		}

		if c.Region == "" {
			return ClustersConfig{}, fmt.Errorf("cluster #%d: region is required", idx+1)
		}

		if c.URL == "" || !strings.HasPrefix(c.URL, "http") {
			return ClustersConfig{}, fmt.Errorf("cluster #%d: url is required", idx+1)
		}
	}

	if cfg.Features.Provider == "" {
		cfg.Features.Provider = ProviderDefault
	}

	return cfg, nil
}

// ReadCloudConfigFromFile reads cloud config from a file.
func ReadCloudConfigFromFile(file string) (ClustersConfig, error) {
	f, err := os.Open(filepath.Clean(file))
	if err != nil {
		return ClustersConfig{}, fmt.Errorf("error reading %s: %v", file, err)
	}
	defer f.Close() // nolint: errcheck

	return ReadCloudConfig(f)
}
