package csi

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
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
