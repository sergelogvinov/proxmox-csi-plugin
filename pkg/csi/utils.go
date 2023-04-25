package csi

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
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

func parseVolumeID(volumeID string) (string, string, string, string, error) {
	volIDParts := strings.Split(volumeID, "/")
	if len(volIDParts) != 4 {
		return "", "", "", "", fmt.Errorf("DeleteVolume Volume ID must be in the format of region/zone/storageName/volume-name")
	}

	region := volIDParts[0]
	zone := volIDParts[1]
	storageName := volIDParts[2]
	pvc := volIDParts[3]

	return region, zone, storageName, pvc, nil
}

func isPvcExists(cl *pxapi.Client, volumeID string) (bool, error) {
	_, zone, storageName, pvc, err := parseVolumeID(volumeID)
	if err != nil {
		return false, err
	}

	vmr := pxapi.NewVmRef(vmID)
	vmr.SetNode(zone)
	vmr.SetVmType("qemu")

	context, err := cl.GetStorageContent(vmr, storageName)
	if err != nil {
		return false, fmt.Errorf("failed to get storage list: %v", err)
	}

	images, ok := context["data"].([]interface{})
	if !ok {
		return false, fmt.Errorf("failed to cast images to map: %v", err)
	}

	volid := fmt.Sprintf("%s:%s", storageName, pvc)

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

func waitForDiskAttach(cl *pxapi.Client, vmr *pxapi.VmRef, lun int) error {
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
