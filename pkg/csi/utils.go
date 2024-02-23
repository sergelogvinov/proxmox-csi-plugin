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
	"fmt"
	"strconv"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

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

	for _, item := range data["data"].([]interface{}) {
		node, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		vmr := pxapi.NewVmRef(vmID)
		vmr.SetNode(node["node"].(string))
		vmr.SetVmType("qemu")

		if _, err := cl.GetStorageStatus(vmr, storageName); err == nil {
			return vmr.Node(), nil
		}
	}

	return "", fmt.Errorf("failed to find node with storage %s", storageName)
}

func getStorageContent(cl *pxapi.Client, vol *volume.Volume) (*storageContent, error) {
	vmr := pxapi.NewVmRef(vmID)
	vmr.SetNode(vol.Node())
	vmr.SetVmType("qemu")

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

		if image["volid"].(string) == volid && image["size"] != nil {
			return &storageContent{
				volID: volid,
				size:  int64(image["size"].(float64)),
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
		return 0, fmt.Errorf(ErrorNotFound)
	}

	return st.size, nil
}

func isVolumeAttached(vmConfig map[string]interface{}, pvc string) (int, bool) {
	if pvc == "" {
		return 0, false
	}

	for lun := 1; lun < 30; lun++ {
		device := fmt.Sprintf("%s%d", deviceNamePrefix, lun)

		if vmConfig[device] != nil && strings.Contains(vmConfig[device].(string), pvc) {
			return lun, true
		}
	}

	return 0, false
}

func waitForVolumeAttach(cl *pxapi.Client, vmr *pxapi.VmRef, lun int) error {
	waited := 0
	for waited < TaskTimeout {
		config, err := cl.GetVmConfig(vmr)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		device := fmt.Sprintf("%s%d", deviceNamePrefix, lun)
		if config[device] != nil {
			return nil
		}

		time.Sleep(TaskStatusCheckInterval * time.Second)
		waited += TaskStatusCheckInterval
	}

	return fmt.Errorf("timeout waiting for disk to attach")
}

func waitForVolumeDetach(_ *pxapi.Client, _ *pxapi.VmRef, _ int) error {
	return nil
}

func createVolume(cl *pxapi.Client, vol *volume.Volume, sizeGB int) error {
	filename := strings.Split(vol.Disk(), "/")
	diskParams := map[string]interface{}{
		"vmid":     vmID,
		"filename": filename[len(filename)-1],
		"size":     fmt.Sprintf("%dG", sizeGB),
	}

	err := cl.CreateVMDisk(vol.Node(), vol.Storage(), fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk()), diskParams)
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

				if err := waitForVolumeAttach(cl, vmr, lun); err != nil {
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

	if err := waitForVolumeDetach(cl, vmr, lun); err != nil {
		return fmt.Errorf("failed to wait for disk detach: %v", err)
	}

	return nil
}

func sizeVolume(cl *pxapi.Client, vol *volume.Volume) (int64, error) {
	vmr := pxapi.NewVmRef(vmID)
	vmr.SetNode(vol.Node())
	vmr.SetVmType("qemu")

	context, err := cl.GetStorageContent(vmr, vol.Storage())
	if err != nil {
		return 0, fmt.Errorf("failed to get storage list: %v", err)
	}

	images, ok := context["data"].([]interface{})
	if !ok {
		return 0, fmt.Errorf("failed to cast images to map: %v", err)
	}

	volid := fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk())

	for i := range images {
		image, ok := images[i].(map[string]interface{})
		if !ok {
			return 0, fmt.Errorf("failed to cast image to map: %v", err)
		}

		if image["volid"].(string) == volid {
			return int64(image["size"].(float64)), nil
		}
	}

	return 0, nil
}
