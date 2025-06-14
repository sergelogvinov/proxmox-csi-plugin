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
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/siderolabs/go-retry/retry"

	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"
)

const (
	// TaskStatusCheckInterval is the interval in seconds to check the status of a task
	TaskStatusCheckInterval = 5
	// TaskTimeout is the timeout in seconds for all task
	TaskTimeout = 30

	// ErrorNotFound not found error message
	ErrorNotFound string = "not found"
)

// Common allocation units
const (
	KiB int64 = 1024
	MiB int64 = 1024 * KiB
	GiB int64 = 1024 * MiB
	TiB int64 = 1024 * GiB
)

type storageContent struct {
	volID string
	size  int64
}

func getNodeWithStorage(cl *pxapi.Client, storageName string) (string, error) {
	data, err := cl.GetNodeList()
	if err != nil {
		return "", fmt.Errorf("failed to get node list: %v", err)
	}

	if data["data"] == nil {
		return "", fmt.Errorf("failed to parce node list: %v", err)
	}

	for _, item := range data["data"].([]interface{}) { //nolint:errcheck
		node, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		vmr := pxapi.NewVmRef(vmID)
		vmr.SetNode(node["node"].(string)) //nolint:errcheck
		vmr.SetVmType("qemu")

		if _, err := cl.GetStorageStatus(vmr, storageName); err == nil {
			return vmr.Node(), nil
		}
	}

	return "", fmt.Errorf("failed to find node with storage %s", storageName)
}

func getVMRefByVolume(cl *pxapi.Client, vol *volume.Volume) (vmr *pxapi.VmRef, err error) {
	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return nil, fmt.Errorf("failed to parse volume vm id: %v", err)
	}

	vmr = pxapi.NewVmRef(id)
	vmr.SetVmType("qemu")

	node := vol.Node()
	if node == "" {
		if id != vmID {
			_, err = cl.GetVmInfo(vmr)
			if err == nil {
				return vmr, nil
			}
		}

		node, err = getNodeWithStorage(cl, vol.Storage())
		if err != nil {
			return nil, err
		}
	}

	if node == "" {
		return nil, fmt.Errorf("failed to find node with storage %s", vol.Storage())
	}

	vmr.SetNode(node)

	return vmr, nil
}

func getVMRefByAttachedVolume(cl *pxapi.Client, vol *volume.Volume) (*pxapi.VmRef, error) {
	vms, err := cl.GetResourceList("vm")
	if err != nil {
		return nil, fmt.Errorf("error get resources %v", err)
	}

	for vmii := range vms {
		vm, ok := vms[vmii].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("failed to cast response to map, vm: %v", vm)
		}

		if vm["type"].(string) != "qemu" { //nolint:errcheck
			continue
		}

		vmr := pxapi.NewVmRef(int(vm["vmid"].(float64))) //nolint:errcheck
		vmr.SetNode(vm["node"].(string))                 //nolint:errcheck
		vmr.SetVmType("qemu")

		config, err := cl.GetVmConfig(vmr)
		if err != nil {
			return nil, err
		}

		if _, exist := isVolumeAttached(config, vol.Disk()); exist {
			return vmr, nil
		}
	}

	return nil, fmt.Errorf("vm with volume %s not found", vol.Disk())
}

func getStorageContent(cl *pxapi.Client, vol *volume.Volume) (*storageContent, error) {
	vmr, err := getVMRefByVolume(cl, vol)
	if err != nil {
		return nil, err
	}

	context, err := cl.GetStorageContent(vmr, vol.Storage())
	if err != nil {
		return nil, fmt.Errorf("failed to get storage list: %v", err)
	}

	images, ok := context["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to cast images to map: %v", err)
	}

	volid := fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk())

	for i := range images {
		image, ok := images[i].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("failed to cast image to map: %v", err)
		}

		if image["volid"].(string) == volid && image["size"] != nil { //nolint:errcheck
			return &storageContent{
				volID: volid,
				size:  int64(image["size"].(float64)), //nolint:errcheck
			}, nil
		}
	}

	return nil, nil
}

func isPvcExists(cl *pxapi.Client, vol *volume.Volume) (bool, error) {
	st, err := getStorageContent(cl, vol)
	if err != nil {
		return false, err
	}

	return st != nil, nil
}

func getVolumeSize(cl *pxapi.Client, vol *volume.Volume) (int64, error) {
	st, err := getStorageContent(cl, vol)
	if err != nil {
		return 0, err
	}

	if st == nil {
		return 0, errors.New(ErrorNotFound)
	}

	return st.size, nil
}

func isVolumeAttached(vmConfig map[string]interface{}, pvc string) (int, bool) {
	if pvc == "" {
		return 0, false
	}

	for lun := 1; lun < 30; lun++ {
		device := fmt.Sprintf("%s%d", deviceNamePrefix, lun)

		if vmConfig[device] != nil && strings.Contains(vmConfig[device].(string), pvc) { //nolint:errcheck
			return lun, true
		}
	}

	return 0, false
}

func waitForVolumeAttach(cl *pxapi.Client, vmr *pxapi.VmRef, lun int, pvc string) error {
	waited := 0
	for waited < TaskTimeout {
		config, err := cl.GetVmConfig(vmr)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		device := fmt.Sprintf("%s%d", deviceNamePrefix, lun)
		if config[device] != nil && strings.Contains(config[device].(string), pvc) { //nolint:errcheck
			return nil
		}

		time.Sleep(TaskStatusCheckInterval * time.Second)
		waited += TaskStatusCheckInterval
	}

	return fmt.Errorf("timeout waiting for disk to attach")
}

func waitForVolumeDetach(cl *pxapi.Client, vmr *pxapi.VmRef, lun int, pvc string) error {
	waited := 0
	for waited < TaskTimeout {
		config, err := cl.GetVmConfig(vmr)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		device := fmt.Sprintf("%s%d", deviceNamePrefix, lun)
		if config[device] == nil {
			return nil
		} else if !strings.Contains(config[device].(string), pvc) { //nolint:errcheck
			return nil
		}

		time.Sleep(TaskStatusCheckInterval * time.Second)
		waited += TaskStatusCheckInterval
	}

	return fmt.Errorf("timeout waiting for disk to detach")
}

func createVolume(cl *pxapi.Client, vol *volume.Volume, sizeBytes int64) error {
	filename := strings.Split(vol.Disk(), "/")

	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volume vm id: %v", err)
	}

	diskParams := map[string]interface{}{
		"vmid":     id,
		"filename": filename[len(filename)-1],
		"size":     fmt.Sprintf("%dM", sizeBytes/MiB),
	}

	err = cl.CreateVMDisk(vol.Node(), vol.Storage(), fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk()), diskParams)
	if err != nil {
		return fmt.Errorf("failed to create vm disk: %v", err)
	}

	return nil
}

func attachVolume(cl *pxapi.Client, vmr *pxapi.VmRef, storageName string, pvc string, options map[string]string) (map[string]string, error) {
	config, err := cl.GetVmConfig(vmr)
	if err != nil {
		return nil, fmt.Errorf("failed to get vm config: %v", err)
	}

	wwm := ""

	lun, exist := isVolumeAttached(config, pvc)
	if exist {
		wwm = hex.EncodeToString([]byte(fmt.Sprintf("PVC-ID%02d", lun)))
	} else {
		for lun = 1; lun < 30; lun++ {
			if config[deviceNamePrefix+strconv.Itoa(lun)] == nil {
				wwm = hex.EncodeToString([]byte(fmt.Sprintf("PVC-ID%02d", lun)))

				options["wwn"] = "0x" + wwm

				opt := make([]string, 0, len(options))
				for k := range options {
					opt = append(opt, fmt.Sprintf("%s=%s", k, options[k]))
				}

				vmParams := map[string]interface{}{
					deviceNamePrefix + strconv.Itoa(lun): fmt.Sprintf("%s:%s,%s", storageName, pvc, strings.Join(opt, ",")),
				}

				_, err = cl.SetVmConfig(vmr, vmParams)
				if err != nil {
					return nil, fmt.Errorf("failed to attach disk: %v, vmParams=%+v", err, vmParams)
				}

				if err := waitForVolumeAttach(cl, vmr, lun, pvc); err != nil {
					return nil, fmt.Errorf("failed to wait for disk attach: %v", err)
				}

				break
			}
		}
	}

	if wwm != "" {
		return map[string]string{
			"DevicePath": "/dev/disk/by-id/wwn-0x" + wwm,
			"lun":        strconv.Itoa(lun),
		}, nil
	}

	return nil, fmt.Errorf("no free lun found")
}

func updateVolume(cl *pxapi.Client, vmr *pxapi.VmRef, storageName string, pvc string, options map[string]string) error {
	config, err := cl.GetVmConfig(vmr)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	lun, exist := isVolumeAttached(config, pvc)
	if !exist {
		return fmt.Errorf("volume does not attached")
	}

	disk := config[deviceNamePrefix+strconv.Itoa(lun)].(string) //nolint:errcheck
	if disk != "" {
		params := strings.Split(disk, ",")
		for _, param := range params {
			kv := strings.Split(param, "=")
			if len(kv) == 2 && options[kv[0]] == "" {
				options[kv[0]] = kv[1]
			}
		}
	}

	opt := make([]string, 0, len(options))
	for k := range options {
		opt = append(opt, fmt.Sprintf("%s=%s", k, options[k]))
	}

	vmParams := map[string]interface{}{
		deviceNamePrefix + strconv.Itoa(lun): fmt.Sprintf("%s:%s,%s", storageName, pvc, strings.Join(opt, ",")),
	}

	_, err = cl.SetVmConfig(vmr, vmParams)
	if err != nil {
		return fmt.Errorf("failed to update disk: %v, vmParams=%+v", err, vmParams)
	}

	return nil
}

func detachVolume(cl *pxapi.Client, vmr *pxapi.VmRef, pvc string) error {
	config, err := cl.GetVmConfig(vmr)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	lun, exist := isVolumeAttached(config, pvc)
	if !exist {
		return nil
	}

	vmParams := map[string]interface{}{
		"idlist": fmt.Sprintf("%s%d", deviceNamePrefix, lun),
	}

	err = cl.Put(vmParams, "/nodes/"+vmr.Node()+"/qemu/"+strconv.Itoa(vmr.VmId())+"/unlink")
	if err != nil {
		return fmt.Errorf("failed to set vm config: %v, vmParams=%+v", err, vmParams)
	}

	if err := waitForVolumeDetach(cl, vmr, lun, pvc); err != nil {
		return fmt.Errorf("failed to wait for disk detach: %v", err)
	}

	return nil
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
