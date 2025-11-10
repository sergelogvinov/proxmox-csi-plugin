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

package csi

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/siderolabs/go-retry/retry"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/provider"

	corev1 "k8s.io/api/core/v1"
)

// Common allocation units
const (
	KiB int64 = 1024
	MiB int64 = 1024 * KiB
	GiB int64 = 1024 * MiB
	TiB int64 = 1024 * GiB
)

const (
	// Group name
	Group = "proxmox.sinextra.dev"

	// AnnotationProxmoxInstanceID is the annotation used to store the Proxmox node virtual machine ID.
	AnnotationProxmoxInstanceID = Group + "/instance-id"
)

// VMLocks is a structure that protects to multiple VMs changes.
type VMLocks struct {
	mux   sync.Mutex
	locks map[string]*sync.Mutex
}

// NewVMLocks creates a new instance of VMLocks.
func NewVMLocks() *VMLocks {
	return &VMLocks{
		locks: make(map[string]*sync.Mutex),
	}
}

// Lock method locks a VM by its name.
func (v *VMLocks) Lock(name string) {
	v.mux.Lock()

	if _, exists := v.locks[name]; !exists {
		v.locks[name] = &sync.Mutex{}
	}

	v.mux.Unlock()
	v.locks[name].Lock()
}

// Unlock method unlocks a VM by its name.
func (v *VMLocks) Unlock(name string) {
	v.mux.Lock()
	defer v.mux.Unlock()

	if lock, exists := v.locks[name]; exists {
		lock.Unlock()
	}
}

// ParseEndpoint parses the endpoint string and returns the scheme and address
func ParseEndpoint(endpoint string) (string, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("could not parse endpoint: %v", err)
	}

	addr := path.Join(u.Host, filepath.FromSlash(u.Path))

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "tcp":
	case "unix":
		addr = path.Join("/", addr)
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return "", "", fmt.Errorf("could not remove unix domain socket %q: %v", addr, err)
		}
	default:
		return "", "", fmt.Errorf("unsupported protocol: %s", scheme)
	}

	return scheme, addr, nil
}

// ProxmoxVMIDbyNode returns the Proxmox VM ID from the specified kubernetes node.
func ProxmoxVMIDbyNode(node *corev1.Node) (int, error) {
	vmID, err := provider.GetVMID(node.Spec.ProviderID)
	if err != nil {
		if vmID, err := strconv.Atoi(node.Annotations[AnnotationProxmoxInstanceID]); err == nil {
			return vmID, nil
		}
	}

	return vmID, err
}

// GetNodeTopology extracts region and zone from the provided labels map.
func GetNodeTopology(labels map[string]string) (region, zone string) {
	region = labels[ProxmoxRegion]
	if region == "" {
		region = labels[corev1.LabelTopologyRegion]
	}

	zone = labels[ProxmoxNode]
	if zone == "" {
		zone = labels[corev1.LabelTopologyZone]
	}

	return region, zone
}

func locationFromTopologyRequirement(tr *proto.TopologyRequirement) (region, zone string) {
	if tr == nil {
		return "", ""
	}

	for _, top := range tr.GetPreferred() {
		segment := top.GetSegments()

		tsr, tsz := GetNodeTopology(segment)
		if tsr != "" && tsz != "" {
			return tsr, tsz
		}

		if tsr != "" && region == "" {
			region = tsr
		}
	}

	for _, top := range tr.GetRequisite() {
		segment := top.GetSegments()

		tsr, tsz := GetNodeTopology(segment)
		if tsr != "" && tsz != "" {
			return tsr, tsz
		}

		if tsr != "" && region == "" {
			region = tsr
		}
	}

	return region, ""
}

func getDevicePath(deviceContext map[string]string) (string, error) {
	sysPath := "/sys/bus/scsi/devices"

	devicePath := deviceContext["DevicePath"]
	if len(devicePath) == 0 {
		return "", fmt.Errorf("DevicePath must be provided")
	}

	deviceWWN := ""
	if strings.HasPrefix(devicePath, "/dev/disk/by-id/wwn-0x") {
		deviceWWN = devicePath[len("/dev/disk/by-id/wwn-0x"):]
	}

	if deviceWWN != "" {
		if dirs, err := os.ReadDir(sysPath); err == nil {
			for _, f := range dirs {
				device := f.Name()

				// /sys/bus/scsi/devices/0:0:0:0
				arr := strings.Split(device, ":")
				if len(arr) < 4 {
					continue
				}

				_, err := strconv.Atoi(arr[3])
				if err != nil {
					continue
				}

				vendorBytes, err := os.ReadFile(filepath.Join(sysPath, device, "vendor"))
				if err != nil {
					continue
				}

				vendor := strings.TrimSpace(string(vendorBytes))
				if strings.ToUpper(vendor) != "QEMU" {
					continue
				}

				wwidBytes, err := os.ReadFile(filepath.Join(sysPath, device, "wwid"))
				if err != nil {
					continue
				}

				wwid := strings.TrimSpace(string(wwidBytes))
				if !strings.HasPrefix(wwid, "naa.") {
					continue
				}

				wwn := wwid[len("naa."):]
				if wwn == deviceWWN {
					if dev, err := os.ReadDir(filepath.Join(sysPath, device, "block")); err == nil {
						if len(dev) > 0 {
							devName := dev[0].Name()

							return fmt.Sprintf("/dev/%s", devName), nil
						}

						return "", fmt.Errorf("no block device found")
					}
				}
			}
		}
	}

	err := retry.Constant(10*time.Second, retry.WithUnits(50*time.Millisecond)).Retry(func() error {
		if _, err := os.Stat(devicePath); err != nil {
			if os.IsNotExist(err) {
				return retry.ExpectedError(err)
			}

			return err
		}

		return nil
	})
	if err != nil {
		if retry.IsTimeout(err) {
			return "", fmt.Errorf("device %s is not found", devicePath)
		}

		return "", err
	}

	return devicePath, nil
}

// RoundUpSizeBytes calculates how many allocation units are needed to accommodate
// a volume of given size. E.g. when user wants 1500MiB volume, while AWS EBS
// allocates volumes in gibibyte-sized chunks,
// RoundUpSizeBytes(1500 * 1024*1024, 1024*1024*1024) returns '2*1024*1024*1024' (2GiB)
// (2 GiB is the smallest allocatable volume that can hold 1500MiB)
func RoundUpSizeBytes(volumeSizeBytes int64, allocationUnitBytes int64) int64 {
	if volumeSizeBytes == 0 {
		return allocationUnitBytes
	}

	return allocationUnitBytes * ((volumeSizeBytes + allocationUnitBytes - 1) / allocationUnitBytes)
}
