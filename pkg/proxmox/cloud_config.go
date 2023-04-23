package cloud

import (
	"io"

	yaml "gopkg.in/yaml.v3"
)

type CloudConfig struct {
	Clusters []struct {
		URL         string `yaml:"url"`
		Insecure    bool   `yaml:"insecure,omitempty"`
		TokenID     string `yaml:"token_id,omitempty"`
		TokenSecret string `yaml:"token_secret,omitempty"`
		Region      string `yaml:"region,omitempty"`
	} `yaml:"clusters,omitempty"`
}

func ReadCloudConfig(config io.Reader) (CloudConfig, error) {
	cfg := CloudConfig{}

	if config != nil {
		if err := yaml.NewDecoder(config).Decode(&cfg); err != nil {
			return CloudConfig{}, err
		}
	}

	return cfg, nil
}
