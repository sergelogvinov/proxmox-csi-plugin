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
	TaskStatusCheckInterval = 5
	TaskTimeout             = 30
)

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

func waitForVolumeDetach(cl *pxapi.Client, vmr *pxapi.VmRef, lun int) error {
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
