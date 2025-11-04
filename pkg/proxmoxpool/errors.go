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

package proxmoxpool

import "github.com/pkg/errors"

var (
	// ErrClustersNotFound is returned when a cluster is not found in the Proxmox
	ErrClustersNotFound = errors.New("clusters not found")
	// ErrHAGroupNotFound is returned when a ha-group is not found in the Proxmox
	ErrHAGroupNotFound = errors.New("ha-group not found")
	// ErrRegionNotFound is returned when a region is not found in the Proxmox
	ErrRegionNotFound = errors.New("region not found")
	// ErrZoneNotFound is returned when a zone is not found in the Proxmox
	ErrZoneNotFound = errors.New("zone not found")
	// ErrInstanceNotFound is returned when an instance is not found in the Proxmox
	ErrInstanceNotFound = errors.New("instance not found")
)
