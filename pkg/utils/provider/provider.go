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

package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
)

const (
	// ProviderName is the name of the Proxmox provider.
	ProviderName = "proxmox"
)

var providerIDRegexp = regexp.MustCompile(`^` + ProviderName + `://([^/]*)/([^/]+)$`)

// GetProviderID returns the magic providerID for kubernetes node.
func GetProviderID(region string, vmr *pxapi.VmRef) string {
	return fmt.Sprintf("%s://%s/%d", ProviderName, region, vmr.VmId())
}

// GetProviderIDFromUUID returns the magic providerID for kubernetes node.
func GetProviderIDFromUUID(uuid string) string {
	return fmt.Sprintf("%s://%s", ProviderName, uuid)
}

// GetVMID returns the VM ID from the providerID.
func GetVMID(providerID string) (int, error) {
	if !strings.HasPrefix(providerID, ProviderName) {
		return 0, fmt.Errorf("foreign providerID or empty \"%s\"", providerID)
	}

	matches := providerIDRegexp.FindStringSubmatch(providerID)
	if len(matches) != 3 {
		return 0, fmt.Errorf("providerID \"%s\" didn't match expected format \"%s://region/InstanceID\"", providerID, ProviderName)
	}

	vmID, err := strconv.Atoi(matches[2])
	if err != nil {
		return 0, fmt.Errorf("InstanceID have to be a number, but got \"%s\"", matches[2])
	}

	return vmID, nil
}

// ParseProviderID returns the VmRef and region from the providerID.
func ParseProviderID(providerID string) (*pxapi.VmRef, string, error) {
	if !strings.HasPrefix(providerID, ProviderName) {
		return nil, "", fmt.Errorf("foreign providerID or empty \"%s\"", providerID)
	}

	matches := providerIDRegexp.FindStringSubmatch(providerID)
	if len(matches) != 3 {
		return nil, "", fmt.Errorf("providerID \"%s\" didn't match expected format \"%s://region/InstanceID\"", providerID, ProviderName)
	}

	vmID, err := strconv.Atoi(matches[2])
	if err != nil {
		return nil, "", fmt.Errorf("InstanceID have to be a number, but got \"%s\"", matches[2])
	}

	return pxapi.NewVmRef(vmID), matches[1], nil
}
