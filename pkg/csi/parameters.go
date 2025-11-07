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
	"reflect"
	"strconv"
	"strings"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/helpers/ptr"
)

const (
	// StorageIDKey is the ID of the Proxmox storage
	StorageIDKey = "storage"
	// StorageFormatKey is the disk format, can be one of "raw", "qcow2", only for file storage devices
	StorageFormatKey = "storageFormat"
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
)

// StorageParameters contains storage parameters
//
// json - tags are used to map the struct to the Kubernetes resource
// cfg  - tags are used to map the struct to the Proxmox API
type StorageParameters struct {
	StorageID     string `json:"storage"`
	StorageFormat string `json:"storageFormat"`

	AIO            string `json:"aio,omitempty"            cfg:"aio"`
	Backup         *bool  `json:"backup,omitempty"         cfg:"backup"`
	Cache          string `json:"cache,omitempty"          cfg:"cache"`
	Discard        string `json:"-"                        cfg:"discard,omitempty"`
	IOThread       bool   `json:"iothread"                 cfg:"iothread,omitempty"`
	IopsRead       *int   `json:"-"                        cfg:"iops_rd,omitempty"`
	IopsWrite      *int   `json:"-"                        cfg:"iops_wr,omitempty"`
	ReadSpeedMbps  *int   `json:"-"                        cfg:"mbps_rd,omitempty"`
	WriteSpeedMbps *int   `json:"-"                        cfg:"mbps_wr,omitempty"`
	SSD            *bool  `json:"ssd,omitempty"            cfg:"ssd,omitempty"`
	ReadOnly       *bool  `json:"-"                        cfg:"ro,omitempty"`
	Iops           *int   `json:"diskIOPS"`
	SpeedMbps      *int   `json:"diskMBps"`
	BlockSize      *int   `json:"blockSize"`
	InodeSize      *int   `json:"inodeSize"`

	Replicate         bool   `json:"replicate,omitempty"   cfg:"replicate"`
	ReplicateSchedule string `json:"replicateSchedule,omitempty"`
	ReplicateZones    string `json:"replicateZones,omitempty"`

	ResizeRequired  *bool `json:"resizeRequired,omitempty"`
	ResizeSizeBytes int64 `json:"resizeSizeBytes,omitempty"`
}

// ModifyVolumeParameters contains parameters to modify a volume
//
// json - tags are used to map the struct to the Kubernetes resource
// cfg  - tags are used to map the struct to the Proxmox API
type ModifyVolumeParameters struct {
	Backup         *bool `json:"backup,omitempty"         cfg:"backup"`
	IopsRead       *int  `json:"-"                        cfg:"iops_rd,omitempty"`
	IopsWrite      *int  `json:"-"                        cfg:"iops_wr,omitempty"`
	ReadSpeedMbps  *int  `json:"-"                        cfg:"mbps_rd,omitempty"`
	WriteSpeedMbps *int  `json:"-"                        cfg:"mbps_wr,omitempty"`
	Iops           *int  `json:"diskIOPS"`
	SpeedMbps      *int  `json:"diskMBps"`

	ReplicateSchedule string `json:"replicateSchedule,omitempty"`
}

// ExtractParameters extracts storage parameters from a map and sets default values.
func ExtractParameters(parameters map[string]string) (StorageParameters, error) {
	p := StorageParameters{
		Backup:    ptr.Ptr(false),
		Replicate: false,
		IOThread:  true,
	}

	err := unmarshalTag(parameters, &p, "json")
	if err != nil {
		return p, err
	}

	if p.SSD != nil && *p.SSD {
		p.Discard = "on"
	}

	if p.Iops != nil && *p.Iops > 0 {
		p.IopsRead = ptr.Ptr(*p.Iops)
		p.IopsWrite = ptr.Ptr(*p.Iops)
	}

	if p.SpeedMbps != nil && *p.SpeedMbps > 0 {
		p.ReadSpeedMbps = ptr.Ptr(*p.SpeedMbps)
		p.WriteSpeedMbps = ptr.Ptr(*p.SpeedMbps)
	}

	return p, nil
}

// ToMap converts storage parameters to kubernetes map of string.
func (p StorageParameters) ToMap() map[string]string {
	m := make(map[string]string)
	mapByTag(p, m, "json")

	return m
}

// ToCFG converts storage parameters to proxmox map of string.
func (p StorageParameters) ToCFG() map[string]string {
	m := make(map[string]string)
	mapByTag(p, m, "cfg")

	return m
}

// ExtractModifyVolumeParameters extracts modify volume parameters from a map.
func ExtractModifyVolumeParameters(parameters map[string]string) (ModifyVolumeParameters, error) {
	p := ModifyVolumeParameters{}

	err := unmarshalTag(parameters, &p, "json")
	if err != nil {
		return p, err
	}

	if p.Iops != nil && *p.Iops > 0 {
		p.IopsRead = ptr.Ptr(*p.Iops)
		p.IopsWrite = ptr.Ptr(*p.Iops)
	}

	if p.SpeedMbps != nil && *p.SpeedMbps > 0 {
		p.ReadSpeedMbps = ptr.Ptr(*p.SpeedMbps)
		p.WriteSpeedMbps = ptr.Ptr(*p.SpeedMbps)
	}

	return p, nil
}

// MergeMap converts ModifyVolumeParameters to a map of string and merge it with the provided map.
func (p ModifyVolumeParameters) MergeMap(orig map[string]string) map[string]string {
	m := map[string]string{}
	mapByTag(p, m, "cfg")

	for key, value := range orig {
		if m[key] != "" {
			continue
		}

		m[key] = value
	}

	return m
}

// ToCFG converts ModifyVolumeParameters to proxmox map of string.
func (p ModifyVolumeParameters) ToCFG() map[string]string {
	m := map[string]string{}
	mapByTag(p, m, "cfg")

	return m
}

func mapByTag(p any, m map[string]string, tag string) {
	val := reflect.ValueOf(p)
	for i := 0; i < val.NumField(); i++ {
		tag := reflect.TypeOf(p).Field(i).Tag.Get(tag)
		if tag == "" || tag == "-" {
			continue
		}

		params := strings.Split(tag, ",")
		tag = params[0]

		fieldValue := val.Field(i)
		if fieldValue.Kind() == reflect.Ptr {
			if fieldValue.IsNil() {
				continue
			}

			fieldValue = fieldValue.Elem()
		}

		switch v := fieldValue.Interface().(type) {
		case string:
			if v != "" {
				m[tag] = v
			}
		case int, int32, int64:
			val := fmt.Sprintf("%d", v)
			if val != "0" {
				m[tag] = val
			}
		case bool:
			if v {
				m[tag] = "1"
			} else {
				m[tag] = "0"
			}
		}
	}
}

func unmarshalTag(m map[string]string, p any, tag string) error {
	ps := reflect.ValueOf(p).Elem()
	for i := 0; i < ps.NumField(); i++ {
		f := ps.Field(i)

		if !f.CanInterface() {
			continue
		}

		tag := ps.Type().Field(i).Tag.Get(tag)
		if tag == "" || tag == "-" {
			continue
		}

		fieldName := strings.Split(tag, ",")[0]
		if v := m[fieldName]; v != "" {
			if f.Kind() == reflect.Ptr {
				switch f.Type().Elem().Kind() { //nolint:exhaustive
				case reflect.String:
					f.Set(reflect.ValueOf(ptr.Ptr(v)))
				case reflect.Bool:
					val, _ := strconv.ParseBool(v) // nolint:errcheck
					f.Set(reflect.ValueOf(ptr.Ptr(val)))
				case reflect.Int, reflect.Int32, reflect.Int64:
					i, err := strconv.ParseInt(v, 10, f.Type().Elem().Bits())
					if err != nil {
						return fmt.Errorf("parameters %s must be a number", fieldName)
					}

					newVal := reflect.New(f.Type().Elem())
					newVal.Elem().SetInt(i)
					f.Set(newVal)
				}
			} else {
				switch f.Kind() { //nolint:exhaustive
				case reflect.String:
					f.Set(reflect.ValueOf(v))
				case reflect.Bool:
					val, _ := strconv.ParseBool(v) // nolint:errcheck
					f.Set(reflect.ValueOf(val))
				case reflect.Int, reflect.Int32, reflect.Int64:
					i, err := strconv.ParseInt(v, 10, f.Type().Bits())
					if err != nil {
						return fmt.Errorf("parameters %s must be a number", fieldName)
					}

					f.SetInt(i)
				}
			}
		}
	}

	return nil
}
