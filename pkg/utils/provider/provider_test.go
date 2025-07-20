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

package provider_test

import (
	"fmt"
	"testing"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/stretchr/testify/assert"

	provider "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/provider"
)

func TestGetProviderID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg                string
		region             string
		vmID               int
		expectedProviderID string
	}{
		{
			msg:                "Valid providerID",
			region:             "region",
			vmID:               123,
			expectedProviderID: "proxmox://region/123",
		},
		{
			msg:                "No region",
			region:             "",
			vmID:               123,
			expectedProviderID: "proxmox:///123",
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			providerID := provider.GetProviderID(testCase.region, pxapi.NewVmRef(testCase.vmID))

			assert.Equal(t, testCase.expectedProviderID, providerID)
		})
	}
}

func TestGetVmID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg           string
		providerID    string
		expectedError error
		expectedvmID  int
	}{
		{
			msg:           "Valid VMID",
			providerID:    "proxmox://region/123",
			expectedError: nil,
			expectedvmID:  123,
		},
		{
			msg:           "Valid VMID with empty region",
			providerID:    "proxmox:///123",
			expectedError: nil,
			expectedvmID:  123,
		},
		{
			msg:           "Invalid providerID format",
			providerID:    "proxmox://123",
			expectedError: fmt.Errorf("providerID \"proxmox://123\" didn't match expected format \"proxmox://region/InstanceID\""),
		},
		{
			msg:           "Non proxmox providerID",
			providerID:    "cloud:///123",
			expectedError: fmt.Errorf("foreign providerID or empty \"cloud:///123\""),
			expectedvmID:  123,
		},
		{
			msg:           "Non proxmox providerID",
			providerID:    "cloud://123",
			expectedError: fmt.Errorf("foreign providerID or empty \"cloud://123\""),
			expectedvmID:  123,
		},
		{
			msg:           "InValid VMID",
			providerID:    "proxmox://region/abc",
			expectedError: fmt.Errorf("InstanceID have to be a number, but got \"abc\""),
			expectedvmID:  0,
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			VMID, err := provider.GetVMID(testCase.providerID)

			if testCase.expectedError != nil {
				assert.NotNil(t, err)
				assert.Equal(t, err.Error(), testCase.expectedError.Error())
			} else {
				assert.Equal(t, testCase.expectedvmID, VMID)
			}
		})
	}
}

func TestParseProviderID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg            string
		providerID     string
		expectedError  error
		expectedvmID   int
		expectedRegion string
	}{
		{
			msg:            "Valid VMID",
			providerID:     "proxmox://region/123",
			expectedError:  nil,
			expectedvmID:   123,
			expectedRegion: "region",
		},
		{
			msg:            "Valid VMID with empty region",
			providerID:     "proxmox:///123",
			expectedError:  nil,
			expectedvmID:   123,
			expectedRegion: "",
		},
		{
			msg:           "Invalid providerID format",
			providerID:    "proxmox://123",
			expectedError: fmt.Errorf("providerID \"proxmox://123\" didn't match expected format \"proxmox://region/InstanceID\""),
		},
		{
			msg:           "Non proxmox providerID",
			providerID:    "cloud:///123",
			expectedError: fmt.Errorf("foreign providerID or empty \"cloud:///123\""),
		},
		{
			msg:           "Non proxmox providerID",
			providerID:    "cloud://123",
			expectedError: fmt.Errorf("foreign providerID or empty \"cloud://123\""),
		},
		{
			msg:           "InValid VMID",
			providerID:    "proxmox://region/abc",
			expectedError: fmt.Errorf("InstanceID have to be a number, but got \"abc\""),
		},
	}

	for _, testCase := range tests {
		testCase := testCase

		t.Run(fmt.Sprint(testCase.msg), func(t *testing.T) {
			t.Parallel()

			vmr, region, err := provider.ParseProviderID(testCase.providerID)

			if testCase.expectedError != nil {
				assert.NotNil(t, err)
				assert.Equal(t, err.Error(), testCase.expectedError.Error())
			} else {
				assert.NotNil(t, vmr)
				assert.Equal(t, testCase.expectedvmID, vmr.VmId())
				assert.Equal(t, testCase.expectedRegion, region)
			}
		})
	}
}
