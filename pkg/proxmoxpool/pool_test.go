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

package proxmoxpool_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
)

func newClusterEnv() []*pxpool.ProxmoxCluster {
	cfg := []*pxpool.ProxmoxCluster{
		{
			URL:         "https://127.0.0.1:8006/api2/json",
			Insecure:    false,
			TokenID:     "user!token-id",
			TokenSecret: "secret",
			Region:      "cluster-1",
		},
		{
			URL:         "https://127.0.0.2:8006/api2/json",
			Insecure:    false,
			TokenID:     "user!token-id",
			TokenSecret: "secret",
			Region:      "cluster-2",
		},
	}

	return cfg
}

func newClusterEnvWithFiles(tokenIDPath, tokenSecretPath string) []*pxpool.ProxmoxCluster {
	cfg := []*pxpool.ProxmoxCluster{
		{
			URL:             "https://127.0.0.1:8006/api2/json",
			Insecure:        false,
			TokenIDFile:     tokenIDPath,
			TokenSecretFile: tokenSecretPath,
			Region:          "cluster-1",
		},
	}

	return cfg
}

func TestNewClient(t *testing.T) {
	cfg := newClusterEnv()
	assert.NotNil(t, cfg)

	pxClient, err := pxpool.NewProxmoxPool([]*pxpool.ProxmoxCluster{})
	assert.NotNil(t, err)
	assert.Nil(t, pxClient)

	pxClient, err = pxpool.NewProxmoxPool(cfg)
	assert.Nil(t, err)
	assert.NotNil(t, pxClient)
}

func TestNewClientWithCredentialsFromFile(t *testing.T) {
	tempDir := t.TempDir()

	tokenIDFile, err := os.CreateTemp(tempDir, "token_id")
	assert.Nil(t, err)

	tokenSecretFile, err := os.CreateTemp(tempDir, "token_secret")
	assert.Nil(t, err)

	_, err = tokenIDFile.WriteString("user!token-id")
	assert.Nil(t, err)
	_, err = tokenSecretFile.WriteString("secret")
	assert.Nil(t, err)

	cfg := newClusterEnvWithFiles(tokenIDFile.Name(), tokenSecretFile.Name())

	pxClient, err := pxpool.NewProxmoxPool(cfg)
	assert.Nil(t, err)
	assert.NotNil(t, pxClient)
	assert.Equal(t, "user!token-id", cfg[0].TokenID)
	assert.Equal(t, "secret", cfg[0].TokenSecret)
}

func TestCheckClusters(t *testing.T) {
	cfg := newClusterEnv()
	assert.NotNil(t, cfg)

	pxClient, err := pxpool.NewProxmoxPool(cfg)
	assert.Nil(t, err)
	assert.NotNil(t, pxClient)

	pxapi, err := pxClient.GetProxmoxCluster("test")
	assert.NotNil(t, err)
	assert.Nil(t, pxapi)
	assert.Equal(t, pxpool.ErrRegionNotFound, err)

	pxapi, err = pxClient.GetProxmoxCluster("cluster-1")
	assert.Nil(t, err)
	assert.NotNil(t, pxapi)

	err = pxClient.CheckClusters(t.Context())
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to initialized proxmox client in region")
}
