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
	"reflect"
	"strings"

	proto "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"

	corev1 "k8s.io/api/core/v1"
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

func locationFromTopologyRequirement(tr *proto.TopologyRequirement) (region, zone string) {
	if tr == nil {
		return "", ""
	}

	for _, top := range tr.Preferred {
		segment := top.GetSegments()

		tsr := segment[corev1.LabelTopologyRegion]
		tsz := segment[corev1.LabelTopologyZone]

		if tsr != "" && tsz != "" {
			return tsr, tsz
		}
	}

	return "", ""
}

func stripSecrets(msg interface{}) string {
	reqValue := reflect.ValueOf(msg)
	reqType := reqValue.Type()

	if reqType.Kind() == reflect.Struct {
		secrets := reqValue.FieldByName("Secrets")
		if secrets.IsValid() && secrets.Kind() == reflect.Map {
			for _, k := range secrets.MapKeys() {
				secrets.SetMapIndex(k, reflect.ValueOf("***stripped***"))
			}
		}
	}

	return fmt.Sprintf("%+v", protosanitizer.StripSecrets(reqValue.Interface()))
}
