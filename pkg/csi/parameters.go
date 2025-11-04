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
// json tags are used to map the struct to the Proxmox API
// cfg tags are used to map the struct to the Kubernetes API
type StorageParameters struct {
	StorageID     string `cfg:"storage"`
	StorageFormat string `cfg:"storageFormat"`

	AIO            string `json:"aio,omitempty"            cfg:"aio"`
	Backup         *bool  `json:"backup,omitempty"         cfg:"backup"`
	Cache          string `json:"cache,omitempty"          cfg:"cache"`
	Discard        string `json:"discard,omitempty"`
	IOThread       bool   `json:"iothread,omitempty"`
	IopsRead       *int   `json:"iops_rd,omitempty"`
	IopsWrite      *int   `json:"iops_wr,omitempty"`
	ReadSpeedMbps  *int   `json:"mbps_rd,omitempty"`
	WriteSpeedMbps *int   `json:"mbps_wr,omitempty"`
	SSD            *bool  `json:"ssd,omitempty"            cfg:"ssd"`
	ReadOnly       *bool  `json:"ro,omitempty"`

	Iops      *int `cfg:"diskIOPS"`
	SpeedMbps *int `cfg:"diskMBps"`
	BlockSize *int `cfg:"blockSize"`
	InodeSize *int `cfg:"inodeSize"`

	Replicate         bool   `json:"replicate,omitempty"   cfg:"replicate"`
	ReplicateSchedule string `cfg:"replicateSchedule,omitempty"`
	ReplicateZones    string `cfg:"replicateZones,omitempty"`

	ResizeRequired  *bool `json:"resizeRequired,omitempty"`
	ResizeSizeBytes int64 `json:"resizeSizeBytes,omitempty"`
}

// ModifyVolumeParameters contains parameters to modify a volume
// json tags are used to map the struct to the Proxmox API
// cfg tags are used to map the struct to the Kubernetes API
type ModifyVolumeParameters struct {
	Backup         *bool `json:"backup,omitempty"          cfg:"backup"`
	IopsRead       *int  `json:"iops_rd,omitempty"`
	IopsWrite      *int  `json:"iops_wr,omitempty"`
	ReadSpeedMbps  *int  `json:"mbps_rd,omitempty"`
	WriteSpeedMbps *int  `json:"mbps_wr,omitempty"`

	Iops      *int `cfg:"diskIOPS"`
	SpeedMbps *int `cfg:"diskMBps"`

	ReplicateSchedule string `cfg:"replicateSchedule,omitempty"`
}

// ExtractAndDefaultParameters extracts storage parameters from a map and sets default values.
func ExtractAndDefaultParameters(parameters map[string]string) (StorageParameters, error) {
	p := StorageParameters{
		Backup:    ptr.Ptr(false),
		Replicate: false,
		IOThread:  true,
	}

	ps := reflect.ValueOf(&p).Elem()
	for i := 0; i < ps.NumField(); i++ {
		f := ps.Field(i)

		if !f.CanInterface() {
			continue
		}

		tag := ps.Type().Field(i).Tag.Get("cfg")
		if tag == "" {
			continue
		}

		fieldName := strings.Split(tag, ",")[0]
		if v := parameters[fieldName]; v != "" {
			if f.Kind() == reflect.Ptr {
				switch f.Type().Elem().Kind() { //nolint:exhaustive
				case reflect.String:
					f.Set(reflect.ValueOf(ptr.Ptr(v)))
				case reflect.Bool:
					f.Set(reflect.ValueOf(ptr.Ptr(v == "true" || v == "1"))) //nolint:goconst
				case reflect.Int, reflect.Int32, reflect.Int64:
					i, err := strconv.Atoi(v)
					if err != nil {
						return p, fmt.Errorf("parameters %s must be a number", fieldName)
					}

					f.Set(reflect.ValueOf(ptr.Ptr(i)))
				}
			} else {
				switch f.Kind() { //nolint:exhaustive
				case reflect.String:
					f.Set(reflect.ValueOf(v))
				case reflect.Bool:
					f.Set(reflect.ValueOf(v == "true" || v == "1")) //nolint:goconst
				case reflect.Int, reflect.Int32, reflect.Int64:
					i, err := strconv.Atoi(v)
					if err != nil {
						return p, fmt.Errorf("parameters %s must be a number", fieldName)
					}

					f.Set(reflect.ValueOf(i))
				}
			}
		}
	}

	if parameters[StorageSSDKey] == "true" {
		p.SSD = ptr.Ptr(true)
		p.Discard = "on"
	}

	if parameters[StorageCacheKey] != "" {
		switch parameters[StorageCacheKey] {
		case "directsync":
			p.Cache = "directsync"
		case "writethrough":
			p.Cache = "writethrough"
		case "writeback":
			p.Cache = "writeback"
		default:
			p.Cache = "none"
		}
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

// ExtractModifyVolumeParameters extracts modify volume parameters from a map.
func ExtractModifyVolumeParameters(parameters map[string]string) (ModifyVolumeParameters, error) {
	p := ModifyVolumeParameters{}

	ps := reflect.ValueOf(&p).Elem()
	for i := 0; i < ps.NumField(); i++ {
		f := ps.Field(i)

		if !f.CanInterface() {
			continue
		}

		tag := ps.Type().Field(i).Tag.Get("cfg")
		if tag == "" {
			continue
		}

		fieldName := strings.Split(tag, ",")[0]
		if v := parameters[fieldName]; v != "" {
			if f.Kind() == reflect.Ptr {
				switch f.Type().Elem().Kind() { //nolint:exhaustive
				case reflect.String:
					f.Set(reflect.ValueOf(ptr.Ptr(v)))
				case reflect.Bool:
					f.Set(reflect.ValueOf(ptr.Ptr(v == "true" || v == "1"))) //nolint:goconst
				case reflect.Int, reflect.Int32, reflect.Int64:
					if i, err := strconv.Atoi(v); err == nil {
						f.Set(reflect.ValueOf(ptr.Ptr(i)))
					}
				}
			}
		}
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

// ToMap converts storage parameters to proxmox map of string.
func (p StorageParameters) ToMap() map[string]string {
	m := make(map[string]string)

	val := reflect.ValueOf(p)
	for i := 0; i < val.NumField(); i++ {
		fieldName := reflect.TypeOf(p).Field(i).Tag.Get("json")
		if fieldName == "" {
			continue
		}

		fieldName = strings.Split(fieldName, ",")[0]

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
				m[fieldName] = v
			}
		case int, int32, int64:
			val := fmt.Sprintf("%d", v)
			if val != "0" {
				m[fieldName] = val
			}
		case bool:
			if v {
				m[fieldName] = "1"
			} else {
				m[fieldName] = "0"
			}
		}
	}

	return m
}

// MergeMap converts ModifyVolumeParameters to a map of string and merge it with the provided map.
func (p ModifyVolumeParameters) MergeMap(orig map[string]string) map[string]string {
	m := map[string]string{}
	for key, value := range orig {
		m[key] = value
	}

	ps := reflect.ValueOf(&p).Elem()
	for i := 0; i < ps.NumField(); i++ {
		f := ps.Field(i)

		if !f.CanInterface() {
			continue
		}

		if f.Kind() == reflect.Ptr {
			if f.IsNil() {
				continue
			}

			f = f.Elem()
		}

		tag := ps.Type().Field(i).Tag.Get("cfg")
		if tag == "" {
			continue
		}

		switch v := f.Interface().(type) {
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

	return m
}

// ToMap converts ModifyVolumeParameters to proxmox map of string and merge it with the provided map.
func (p ModifyVolumeParameters) ToMap() map[string]string {
	m := map[string]string{}

	ps := reflect.ValueOf(&p).Elem()
	for i := 0; i < ps.NumField(); i++ {
		f := ps.Field(i)

		if !f.CanInterface() {
			continue
		}

		if f.Kind() == reflect.Ptr {
			if f.IsNil() {
				continue
			}

			f = f.Elem()
		}

		tag := ps.Type().Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}

		tag = strings.Split(tag, ",")[0]

		switch v := f.Interface().(type) {
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

	return m
}
