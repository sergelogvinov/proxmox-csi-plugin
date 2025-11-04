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

package config_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	providerconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
)

func TestReadCloudConfig(t *testing.T) {
	cfg, err := providerconfig.ReadCloudConfig(nil)
	assert.Nil(t, err)
	assert.NotNil(t, cfg)

	tests := []struct {
		msg           string
		config        io.Reader
		expectedError string
		expected      *providerconfig.ClustersConfig
	}{
		{
			msg: "invalid config",
			config: strings.NewReader(`
clusters:
  test: false
`),
			expectedError: providerconfig.ErrInvalidCloudConfig.Error(),
		},
		{
			msg: "non full config",
			config: strings.NewReader(`
clusters:
- url: abcd
  region: cluster-1
`),
			expectedError: providerconfig.ErrAuthCredentialsMissing.Error(),
		},
		{
			msg: "missing region",
			config: strings.NewReader(`
clusters:
- url: https://example.com
  token_id: "user!token-id"
  token_secret: "secret"
`),
			expectedError: providerconfig.ErrMissingPVERegion.Error(),
		},
		// repeat tests for test on bottom
		{
			msg: "empty url",
			config: strings.NewReader(`
clusters:
- region: test
  insecure: false
  username: "user@pam"
  password: "secret"
`),
			expectedError: providerconfig.ErrMissingPVEAPIURL.Error(),
		},
		{
			msg: "invalid url protocol",
			config: strings.NewReader(`
clusters:
  - url: quic://example.com
    insecure: false
    region: test
    username: "user@pam"
    password: "secret"
`),
			expectedError: providerconfig.ErrMissingPVEAPIURL.Error(),
		},
		{
			msg: "conflicting auth methods",
			config: strings.NewReader(`
clusters:
  - url: https://example.com
    insecure: false
    username: "user@pam"
    password: "secret"
    token_id: "ha"
    token_secret: "secret"
    region: cluster-1
`),
			expectedError: providerconfig.ErrInvalidAuthCredentials.Error(),
		},
		{
			msg: "valid config with one cluster auth methods",
			config: strings.NewReader(`
clusters:
  - url: https://example.com
    insecure: false
    username: "user@pam"
    password: "secret"
    region: cluster-1
`),
			expected: &providerconfig.ClustersConfig{
				Features: providerconfig.ClustersFeatures{
					Provider: providerconfig.ProviderDefault,
				},
				Clusters: []*pxpool.ProxmoxCluster{
					{
						URL:      "https://example.com",
						Insecure: false,
						Username: "user@pam",
						Password: "secret",
						Region:   "cluster-1",
					},
				},
			},
		},
		{
			msg: "valid config with one cluster and secret_file",
			config: strings.NewReader(`
clusters:
  - url: https://example.com
    insecure: false
    token_id_file: "/etc/proxmox-secrets/cluster1/token_id"
    token_secret_file: "/etc/proxmox-secrets/cluster1/token_secret"
    region: cluster-1
`),
			expected: &providerconfig.ClustersConfig{
				Features: providerconfig.ClustersFeatures{
					Provider: providerconfig.ProviderDefault,
				},
				Clusters: []*pxpool.ProxmoxCluster{
					{
						URL:             "https://example.com",
						Insecure:        false,
						TokenIDFile:     "/etc/proxmox-secrets/cluster1/token_id",
						TokenSecretFile: "/etc/proxmox-secrets/cluster1/token_secret",
						Region:          "cluster-1",
					},
				},
			},
		},
		{
			msg: "provider capmox",
			config: strings.NewReader(`
features:
  provider: capmox
clusters:
  - url: https://example.com
    insecure: false
    token_id: "ha"
    token_secret: "secret"
    region: cluster-1
`),
			expected: &providerconfig.ClustersConfig{
				Features: providerconfig.ClustersFeatures{
					Provider: providerconfig.ProviderCapmox,
				},
				Clusters: []*pxpool.ProxmoxCluster{
					{
						URL:         "https://example.com",
						Insecure:    false,
						TokenID:     "ha",
						TokenSecret: "secret",
						Region:      "cluster-1",
					},
				},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.msg, func(t *testing.T) {
			cfg, err := providerconfig.ReadCloudConfig(testCase.config)
			if testCase.expectedError != "" {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), testCase.expectedError)
			} else {
				assert.Nil(t, err)
				assert.NotNil(t, cfg)

				if testCase.expected != nil {
					assert.Equal(t, testCase.expected, &cfg)
				}
			}
		})
	}
}

func TestReadCloudConfigFromFile(t *testing.T) {
	cfg, err := providerconfig.ReadCloudConfigFromFile("testdata/cloud-config.yaml")
	assert.NotNil(t, err)
	assert.EqualError(t, err, "error reading testdata/cloud-config.yaml: open testdata/cloud-config.yaml: no such file or directory")
	assert.NotNil(t, cfg)

	cfg, err = providerconfig.ReadCloudConfigFromFile("../../hack/proxmox-config.yaml")
	assert.Nil(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, 2, len(cfg.Clusters))
}
