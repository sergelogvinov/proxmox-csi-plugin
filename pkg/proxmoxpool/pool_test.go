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
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/jarcoal/httpmock"
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

	pClient, err := pxpool.NewProxmoxPool([]*pxpool.ProxmoxCluster{}, nil)
	assert.NotNil(t, err)
	assert.Nil(t, pClient)

	pClient, err = pxpool.NewProxmoxPool(cfg, nil)
	assert.Nil(t, err)
	assert.NotNil(t, pClient)
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

	pxClient, err := pxpool.NewProxmoxPool(cfg, nil)
	assert.Nil(t, err)
	assert.NotNil(t, pxClient)
	assert.Equal(t, "user!token-id", cfg[0].TokenID)
	assert.Equal(t, "secret", cfg[0].TokenSecret)
}

func TestCheckClusters(t *testing.T) {
	cfg := newClusterEnv()
	assert.NotNil(t, cfg)

	pClient, err := pxpool.NewProxmoxPool(cfg, nil)
	assert.Nil(t, err)
	assert.NotNil(t, pClient)

	pxapi, err := pClient.GetProxmoxCluster("test")
	assert.NotNil(t, err)
	assert.Nil(t, pxapi)
	assert.Equal(t, "proxmox cluster test not found", err.Error())

	pxapi, err = pClient.GetProxmoxCluster("cluster-1")
	assert.Nil(t, err)
	assert.NotNil(t, pxapi)

	err = pClient.CheckClusters(t.Context())
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to initialized proxmox client in region")
}

func TestFindVMByNameNonExist(t *testing.T) {
	cfg := newClusterEnv()
	assert.NotNil(t, cfg)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/cluster/resources",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"node": "node-1",
						"type": "qemu",
						"vmid": 100,
						"name": "test1-vm",
					},
				},
			})
		},
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.2:8006/api2/json/cluster/resources",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"node": "node-2",
						"type": "qemu",
						"vmid": 100,
						"name": "test2-vm",
					},
				},
			})
		},
	)

	pClient, err := pxpool.NewProxmoxPool(cfg, &http.Client{})
	assert.Nil(t, err)
	assert.NotNil(t, pClient)

	vmr, cluster, err := pClient.FindVMByName(t.Context(), "non-existing-vm")
	assert.NotNil(t, err)
	assert.Equal(t, "", cluster)
	assert.Nil(t, vmr)
	assert.Contains(t, err.Error(), "vm 'non-existing-vm' not found")
}

func TestFindVMByNameExist(t *testing.T) {
	cfg := newClusterEnv()
	assert.NotNil(t, cfg)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("GET", "https://127.0.0.1:8006/api2/json/cluster/resources",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"node": "node-1",
					"type": "qemu",
					"vmid": 100,
					"name": "test1-vm",
				},
			},
		}),
	)

	httpmock.RegisterResponder("GET", "https://127.0.0.2:8006/api2/json/cluster/resources",
		func(_ *http.Request) (*http.Response, error) {
			return httpmock.NewJsonResponse(200, map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"node": "node-2",
						"type": "qemu",
						"vmid": 100,
						"name": "test2-vm",
					},
				},
			})
		},
	)

	pClient, err := pxpool.NewProxmoxPool(cfg, &http.Client{})
	assert.Nil(t, err)
	assert.NotNil(t, pClient)

	tests := []struct {
		msg             string
		vmName          string
		expectedError   error
		expectedVMID    int
		expectedCluster string
	}{
		{
			msg:           "vm not found",
			vmName:        "non-existing-vm",
			expectedError: fmt.Errorf("vm 'non-existing-vm' not found"),
		},
		{
			msg:             "Test1-VM",
			vmName:          "test1-vm",
			expectedVMID:    100,
			expectedCluster: "cluster-1",
		},
		{
			msg:             "Test2-VM",
			vmName:          "test2-vm",
			expectedVMID:    100,
			expectedCluster: "cluster-2",
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			vmr, cluster, err := pClient.FindVMByName(t.Context(), testCase.vmName)

			if testCase.expectedError == nil {
				assert.Nil(t, err)
				assert.NotNil(t, vmr)
				assert.Equal(t, testCase.expectedVMID, vmr.VmId())
				assert.Equal(t, testCase.expectedCluster, cluster)
			} else {
				assert.NotNil(t, err)
				assert.Equal(t, "", cluster)
				assert.Nil(t, vmr)
				assert.Contains(t, err.Error(), "vm 'non-existing-vm' not found")
			}
		})
	}
}
