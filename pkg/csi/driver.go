/*
Copyright 2023 sergelogvinov.

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

// Package csi contains the CSI driver implementation
package csi

const (
	// DriverName is the name of the CSI driver
	DriverName = "csi.proxmox.com"
	// DriverVersion is the version of the CSI driver
	DriverVersion = "0.0.1"

	// MaxVolumesPerNode is the maximum number of volumes that can be attached to a node
	MaxVolumesPerNode = 16
	// MinVolumeSize is the minimum size of a volume
	MinVolumeSize = 1 // GB
	// DefaultVolumeSize is the default size of a volume
	DefaultVolumeSize = MinVolumeSize
)
