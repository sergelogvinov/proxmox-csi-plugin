package cloud

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	yaml "gopkg.in/yaml.v3"
)

// ClustersConfig is proxmox multi-cluster cloud config.
type ClustersConfig struct {
	Clusters []struct {
		URL         string `yaml:"url"`
		Insecure    bool   `yaml:"insecure,omitempty"`
		TokenID     string `yaml:"token_id,omitempty"`
		TokenSecret string `yaml:"token_secret,omitempty"`
		Region      string `yaml:"region,omitempty"`
	} `yaml:"clusters,omitempty"`
}

// ReadCloudConfig reads cloud config from a reader.
func ReadCloudConfig(config io.Reader) (ClustersConfig, error) {
	cfg := ClustersConfig{}

	if config != nil {
		if err := yaml.NewDecoder(config).Decode(&cfg); err != nil {
			return ClustersConfig{}, err
		}
	}

	return cfg, nil
}

// ReadFromFileCloudConfig reads cloud config from a file.
func ReadFromFileCloudConfig(cloudConfig string) (ClustersConfig, error) {
	f, err := os.Open(filepath.Clean(cloudConfig))
	if err != nil {
		return ClustersConfig{}, fmt.Errorf("error reading %s: %v", cloudConfig, err)
	}
	defer f.Close() // nolint: errcheck

	cfg := ClustersConfig{}

	if f != nil {
		if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
			return ClustersConfig{}, err
		}
	}

	return cfg, nil
}
