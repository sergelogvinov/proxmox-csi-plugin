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

package node

import (
	"fmt"
	"slices"
	"strings"

	"github.com/digitalocean/go-smbios/smbios"
)

// SystemInformation holds SMBIOS system information.
type SystemInformation struct {
	// Manufacturer returns the system manufacturer.
	Manufacturer string
	// ProductName returns the system product name.
	ProductName string
	// Version returns the system version.
	Version string
	// SerialNumber returns the system serial number.
	SerialNumber string
	// SKUNumber returns the system SKU number.
	SKUNumber string
	// Family returns the system family.
	Family string
}

// GetSMBIOSInfo retrieves the SMBIOS system information.
func GetSMBIOSInfo() (SystemInformation, error) {
	rc, _, err := smbios.Stream()
	if err != nil {
		return SystemInformation{}, fmt.Errorf("failed to access smbios: %v", err)
	}
	defer rc.Close() //nolint: errcheck

	d := smbios.NewDecoder(rc)

	structures, err := d.Decode()
	if err != nil {
		return SystemInformation{}, fmt.Errorf("failed to decode smbios: %v", err)
	}

	for _, structure := range structures {
		if structure.Header.Type == 1 {
			return SystemInformation{
				Manufacturer: getStringOrEmpty(structure, 0x04),
				ProductName:  getStringOrEmpty(structure, 0x05),
				Version:      getStringOrEmpty(structure, 0x06),
				SerialNumber: getStringOrEmpty(structure, 0x07),
				SKUNumber:    getStringOrEmpty(structure, 0x19),
				Family:       getStringOrEmpty(structure, 0x1A),
			}, nil
		}
	}

	return SystemInformation{}, nil
}

func getStringOrEmpty(s *smbios.Structure, offset int) string {
	index := getByte(s, offset)

	if index == 0 || int(index) > len(s.Strings) {
		return ""
	}

	trimmed := strings.ToLower(strings.TrimSpace(s.Strings[index-1]))
	if slices.Contains([]string{
		"to be filled by o.e.m.",
		"default string",
		"system product name",
		"system serial number",
		"not specified",
	}, trimmed) {
		return ""
	}

	return trimmed
}

func getByte(s *smbios.Structure, offset int) uint8 {
	// the `Formatted` byte slice is missing the first 4 bytes of the structure that are stripped out as header info.
	index := offset - 4
	if index >= len(s.Formatted) {
		return 0
	}

	return s.Formatted[index]
}
