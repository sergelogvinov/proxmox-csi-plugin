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

// Package csi contains the CSI driver implementation
package csi

const (
	// DriverName is the name of the CSI driver
	DriverName = "csi.proxmox.sinextra.dev"
	// DriverVersion is the version of the CSI driver
	DriverVersion = "0.3.0"

	// StorageIDKey is the ID of the Proxmox storage
	StorageIDKey = "storage"
	// StorageCacheKey is the cache type, can be one of "directsync", "none", "writeback", "writethrough"
	StorageCacheKey = "cache"
	// StorageSSDKey is it ssd disk
	StorageSSDKey = "ssd"

	// StorageDiskIOPSKey is maximum r/w I/O in operations per second
	StorageDiskIOPSKey = "diskIOPS"
	// StorageDiskMBpsKey is maximum r/w throughput in MB/s
	StorageDiskMBpsKey = "diskMBps"

	// StorageBlockSizeKey the block size when formatting a volume
	StorageBlockSizeKey = "blockSize"

	// StorageInodeSizeKey the inode size when formatting a volume
	StorageInodeSizeKey = "inodeSize"

	// MaxVolumesPerNode is the maximum number of volumes that can be attached to a node
	MaxVolumesPerNode = 16
	// MinVolumeSize is the minimum size of a volume
	MinVolumeSize = 1 // GB
	// DefaultVolumeSize is the default size of a volume
	DefaultVolumeSize = 10 // GB

	// EncryptionPassphraseKey is the encryption passphrase secret key
	EncryptionPassphraseKey = "encryption-passphrase"
)

// constants for fstypes
const (
	// FSTypeExt4 represents the ext4 filesystem type
	FSTypeExt4 = "ext4"
	// FSTypeXfs represents the xfs filesystem type
	FSTypeXfs = "xfs"
)
