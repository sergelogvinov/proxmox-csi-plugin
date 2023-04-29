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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"

	"k8s.io/klog/v2"
)

const (
	// TaskStatusCheckInterval is the interval in seconds to check the status of a task
	TaskStatusCheckInterval = 5
	// TaskTimeout is the timeout in seconds for all task
	TaskTimeout = 30
)

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

func isPvcExists(cl *pxapi.Client, vol *volume.Volume) (bool, error) {
	vmr := pxapi.NewVmRef(vmID)
	vmr.SetNode(vol.Node())
	vmr.SetVmType("qemu")

	context, err := cl.GetStorageContent(vmr, vol.Storage())
	if err != nil {
		return false, fmt.Errorf("failed to get storage list: %v", err)
	}

	images, ok := context["data"].([]interface{})
	if !ok {
		return false, fmt.Errorf("failed to cast images to map: %v", err)
	}

	volid := fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk())

	for i := range images {
		image, ok := images[i].(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("failed to cast image to map: %v", err)
		}

		if image["volid"].(string) == volid {
			return true, nil
		}
	}

	return false, nil
}

func isVolumeAttached(vmConfig map[string]interface{}, pvc string) (int, bool) {
	if pvc == "" {
		return 0, false
	}

	for lun := 1; lun < 30; lun++ {
		device := fmt.Sprintf("scsi%d", lun)

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

		device := fmt.Sprintf("scsi%d", lun)
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
	diskParams := map[string]interface{}{
		"vmid":     vmID,
		"filename": vol.Disk(),
		"size":     fmt.Sprintf("%dG", sizeGB),
	}

	err := cl.CreateVMDisk(vol.Node(), vol.Storage(), fmt.Sprintf("%s:%s", vol.Storage(), vol.Disk()), diskParams)
	if err != nil {
		klog.Errorf("failed to create vm disk: %v", err)

		return fmt.Errorf("failed to create vm disk: %v", err)
	}

	return nil
}

func attachVolume(cl *pxapi.Client, vmr *pxapi.VmRef, storageName string, pvc string, options map[string]string) (map[string]string, error) {
	config, err := cl.GetVmConfig(vmr)
	if err != nil {
		klog.Errorf("failed to get vm config: %v", err)

		return nil, err
	}

	wwm := ""

	lun, exist := isVolumeAttached(config, pvc)
	if exist {
		klog.V(3).Infof("volume %s already attached", pvc)

		wwm = hex.EncodeToString([]byte(fmt.Sprintf("PVC-ID%02d", lun)))
	} else {
		for lun = 1; lun < 30; lun++ {
			if config["scsi"+strconv.Itoa(lun)] == nil {
				wwm = hex.EncodeToString([]byte(fmt.Sprintf("PVC-ID%02d", lun)))

				options["wwn"] = "0x" + wwm

				opt := make([]string, 0, len(options))
				for k := range options {
					opt = append(opt, fmt.Sprintf("%s=%s", k, options[k]))
				}

				vmParams := map[string]interface{}{
					"scsi" + strconv.Itoa(lun): fmt.Sprintf("%s:%s,%s", storageName, pvc, strings.Join(opt, ",")),
				}

				klog.Infof("attaching disk: %+v", vmParams)

				_, err = cl.SetVmConfig(vmr, vmParams)
				if err != nil {
					klog.Errorf("failed to attach disk: %v, vmParams=%+v", err, vmParams)

					return nil, err
				}

				if err := waitForVolumeAttach(cl, vmr, lun); err != nil {
					klog.Errorf("failed to wait for disk attach: %v", err)

					return nil, err
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
		"idlist": fmt.Sprintf("scsi%d", lun),
	}

	err = cl.Put(vmParams, "/nodes/"+vmr.Node()+"/qemu/"+strconv.Itoa(vmr.VmId())+"/unlink")
	if err != nil {
		klog.Errorf("failed to set vm config: %v, vmParams=%+v", err, vmParams)

		return err
	}

	if err := waitForVolumeDetach(cl, vmr, lun); err != nil {
		klog.Errorf("failed to wait for disk detach: %v", err)

		return err
	}

	return nil
}
