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
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/siderolabs/go-retry/retry"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	proxmoxrest "github.com/sergelogvinov/go-proxmox-rest"
	pxcluster "github.com/sergelogvinov/go-proxmox-rest/cluster"
	"github.com/sergelogvinov/go-proxmox-rest/cluster/replication"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/storage"
	"github.com/sergelogvinov/go-proxmox-rest/nodes/tasks"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/metrics"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	v1 "k8s.io/api/core/v1"
)

const (
	// TaskStatusCheckInterval is the interval in seconds to check the status of a task
	TaskStatusCheckInterval = 5
	// TaskTimeout is the timeout in seconds for all task
	TaskTimeout = 30

	// ErrorNotFound not found error message
	ErrorNotFound string = "not found"

	// guestTypeQemu restricts a cluster.ListFilter to QEMU guests, excluding LXC containers.
	guestTypeQemu = "qemu"
)

// errVirtualMachineNotFound indicates that no matching VM was found in the Proxmox
// cluster. go-proxmox-rest has no sentinel of its own for this (see IsNotFound);
// this package keeps one so call sites can distinguish "not published"/"already
// gone" from a real API error, the same way they could against the legacy client.
var errVirtualMachineNotFound = errors.New("virtual machine not found")

// findVMNode resolves the Proxmox node a guest currently runs on from its VMID.
func findVMNode(ctx context.Context, cl *proxmoxrest.Client, vmid int) (string, error) {
	resources, err := cl.Cluster().Resources().List(ctx, pxcluster.ListFilter{
		Type:      pxcluster.ResourceTypeVM,
		GuestType: guestTypeQemu,
		VMID:      vmid,
	})
	if err != nil {
		return "", err
	}

	if len(resources) == 0 {
		return "", errVirtualMachineNotFound
	}

	return resources[0].Node, nil
}

// findVMByNode searches every configured cluster for a VM whose name is
// prefixed by node.Name and whose SMBIOS UUID matches the node's reported
// SystemUUID. Narrowing by name first keeps UUID resolution cheap:
// pxpool.WithUUID only issues a Config call against candidates that already
// passed the name filter (or resolves instantly via the pool's UUID index).
func findVMByNode(ctx context.Context, pool *pxpool.ProxmoxPool, node *v1.Node) (vmID int, region string, err error) {
	for _, name := range pool.List() {
		vms, err := pool.Cluster(name).List(ctx, pxpool.ResourceKindVM,
			pxpool.WithMatch(func(rs *pxcluster.Resource) (bool, error) {
				return strings.HasPrefix(rs.Name, node.Name), nil
			}),
			pxpool.WithUUID(node.Status.NodeInfo.SystemUUID),
		)
		if err != nil {
			return 0, "", err
		}

		if len(vms) > 0 {
			return vms[0].VMID, name, nil
		}
	}

	return 0, "", pxpool.ErrInstanceNotFound
}

// findVMByUUID searches every configured cluster for a VM whose SMBIOS
// UUID matches uuid.
func findVMByUUID(ctx context.Context, pool *pxpool.ProxmoxPool, uuid string) (vmID int, region string, err error) {
	for _, name := range pool.List() {
		vms, err := pool.Cluster(name).List(ctx, pxpool.ResourceKindVM, pxpool.WithUUID(uuid))
		if err != nil {
			return 0, "", err
		}

		if len(vms) > 0 {
			return vms[0].VMID, name, nil
		}
	}

	return 0, "", pxpool.ErrInstanceNotFound
}

// storageResource returns the /cluster/resources entry for storageID.
func storageResource(ctx context.Context, cl *proxmoxrest.Client, storageID string) (*pxcluster.Resource, error) {
	resources, err := cl.Cluster().Resources().List(ctx, pxcluster.ListFilter{
		Type:      pxcluster.ResourceTypeStorage,
		StorageID: storageID,
	})
	if err != nil {
		return nil, err
	}

	if len(resources) == 0 {
		return nil, errors.New(ErrorNotFound)
	}

	return &resources[0], nil
}

// storageNodes returns the nodes storageID is currently available on.
func storageNodes(ctx context.Context, cl *proxmoxrest.Client, storageID string) ([]string, error) {
	resources, err := cl.Cluster().Resources().List(ctx, pxcluster.ListFilter{
		Type:      pxcluster.ResourceTypeStorage,
		StorageID: storageID,
		Match: func(rs *pxcluster.Resource) (bool, error) {
			return rs.Status == "available", nil
		},
	})
	if err != nil {
		return nil, err
	}

	nodes := make([]string, 0, len(resources))
	for _, rs := range resources {
		nodes = append(nodes, rs.Node)
	}

	return nodes, nil
}

// volumeNodes returns the candidate nodes for vol: its own node if set, otherwise
// every node the volume's storage is currently available on.
func volumeNodes(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume) ([]string, error) {
	if vol.Node() != "" {
		return []string{vol.Node()}, nil
	}

	nodes, err := storageNodes(ctx, cl, vol.Storage())
	if err != nil {
		return nil, fmt.Errorf("failed to find zones for storage %s: %v", vol.Storage(), err)
	}

	return nodes, nil
}

func getNodeForVolume(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume) (string, error) {
	nodes, err := volumeNodes(ctx, cl, vol)
	if err != nil {
		return "", err
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("failed to find best zone for storage %s", vol.Storage())
	}

	return nodes[0], nil
}

func getVMByAttachedVolume(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume) (int, int, error) {
	nodes, err := volumeNodes(ctx, cl, vol)
	if err != nil {
		return 0, 0, err
	}

	if len(nodes) == 0 {
		return 0, 0, fmt.Errorf("failed to find best zone: no nodes with the storage %s", vol.Storage())
	}

	lun := 0

	resources, err := cl.Cluster().Resources().List(ctx, pxcluster.ListFilter{
		Type:      pxcluster.ResourceTypeVM,
		GuestType: guestTypeQemu,
		Match: func(rs *pxcluster.Resource) (bool, error) {
			// Skip the storage owner VM (e.g., 9999), as the VM uses for the replications
			if vol.VMID() == strconv.Itoa(rs.VMID) {
				return false, nil
			}

			if !slices.Contains(nodes, rs.Node) {
				return false, nil
			}

			cfg, err := cl.Nodes(rs.Node).Qemu().Config(ctx, rs.VMID, nil)
			if err != nil {
				return false, err
			}

			l, exist := isVolumeAttached(cfg, vol.Disk())
			if exist {
				lun = l
			}

			return exist, nil
		},
	})
	if err != nil {
		return 0, lun, err
	}

	if len(resources) == 0 {
		return 0, 0, errVirtualMachineNotFound
	}

	if vol.Node() == "" {
		vol.SetNode(resources[0].Node)
	}

	return resources[0].VMID, lun, nil
}

func getStorageContent(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume) (*storage.Volume, error) {
	if vol.Node() == "" {
		return nil, errors.New("node is required")
	}

	if _, err := cl.Nodes(vol.Node()).Storage().Status(ctx, vol.Storage()); err != nil {
		if proxmoxrest.IsNotFound(err) {
			return nil, errors.New(ErrorNotFound)
		}

		return nil, err
	}

	contents, err := cl.Nodes(vol.Node()).Storage().Content(vol.Storage()).List(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range contents {
		if contents[i].VolID == vol.VolID() {
			return &contents[i], nil
		}
	}

	return nil, nil
}

func getVolumeSize(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume) (int64, error) {
	st, err := getStorageContent(ctx, cl, vol)
	if err != nil {
		return 0, err
	}

	if st == nil {
		return 0, errors.New(ErrorNotFound)
	}

	return st.Size, nil
}

// isVolumeAttached reports whether pvc is attached to a guest with the given
// configuration, returning the SCSI lun it's attached at.
func isVolumeAttached(cfg *qemu.Config, pvc string) (int, bool) {
	if pvc == "" {
		return 0, false
	}

	for lun, disk := range cfg.SCSI {
		if strings.Contains(disk.File, pvc) {
			return lun, true
		}
	}

	return 0, false
}

// driveOptions applies the property overrides in options (as built by
// StorageParameters.ToCFG/ModifyVolumeParameters.ToCFG) onto drive, leaving every
// other field untouched.
func driveOptions(drive qemu.Drive, options map[string]string) qemu.Drive {
	if v, ok := options["aio"]; ok {
		drive.AIO = v
	}

	if v, ok := options["cache"]; ok {
		drive.Cache = v
	}

	if v, ok := options["discard"]; ok {
		drive.Discard = v
	}

	if v, ok := options["backup"]; ok {
		drive.Backup = new(v == "1")
	}

	if v, ok := options["iothread"]; ok {
		drive.IOThread = new(v == "1")
	}

	if v, ok := options["ssd"]; ok {
		drive.SSD = new(v == "1")
	}

	if v, ok := options["ro"]; ok {
		drive.RO = new(v == "1")
	}

	if v, ok := options["replicate"]; ok {
		drive.Replicate = new(v == "1")
	}

	if v, ok := options["iops_rd"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			drive.IOPSRD = new(n)
		}
	}

	if v, ok := options["iops_wr"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			drive.IOPSWR = new(n)
		}
	}

	if v, ok := options["mbps_rd"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			drive.MBPSRD = new(n)
		}
	}

	if v, ok := options["mbps_wr"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			drive.MBPSWR = new(n)
		}
	}

	return drive
}

func prepareReplication(ctx context.Context, cl *proxmoxrest.Client, node string, name string, vmID int) (int, error) {
	resources, err := cl.Cluster().Resources().List(ctx, pxcluster.ListFilter{
		Type: pxcluster.ResourceTypeVM,
		Match: func(rs *pxcluster.Resource) (bool, error) {
			return rs.Name == name, nil
		},
	})
	if err != nil {
		return 0, err
	}

	if len(resources) > 0 {
		return resources[0].VMID, nil
	}

	id, err := cl.Cluster().NextID(ctx, vmID+1)
	if err != nil {
		return 0, err
	}

	cfg := defaultVMConfig()
	cfg.Name = name

	mc := metrics.NewMetricContext("createVm")

	upid, err := cl.Nodes(node).Qemu().Create(ctx, &qemu.CreateOptions{
		Config: *cfg,
		VMID:   id,
	})
	if mc.ObserveRequest(err) != nil {
		return 0, err
	}

	if upid != "" {
		if err := cl.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
			return 0, err
		}
	}

	return id, nil
}

func createReplication(ctx context.Context, cl *proxmoxrest.Client, id int, vol *volume.Volume, params StorageParameters) error {
	cfg := map[string]string{
		"replicate": "1",
		"backup":    "1",
	}
	if _, err := attachVolume(ctx, cl, id, vol, cfg); err != nil {
		return err
	}

	schedule := "*/15"
	if params.ReplicateSchedule != "" {
		schedule = params.ReplicateSchedule
	}

	for i, z := range strings.Split(params.ReplicateZones, ",") {
		if z == vol.Node() {
			continue
		}

		opts := &replication.JobOptions{
			ID:       fmt.Sprintf("%d-%d", id, i),
			Type:     replication.TypeLocal,
			Target:   z,
			Schedule: new(schedule),
			Disable:  new(false),
			Comment:  new("CSI Replication for Persistent Volume"),
		}

		if err := cl.Cluster().Replication().Create(ctx, opts); err != nil {
			return fmt.Errorf("failed to create replication: %v, opts=%+v", err, opts)
		}
	}

	return nil
}

func migrateReplication(ctx context.Context, cl *proxmoxrest.Client, target int, vol *volume.Volume, vmID int) error {
	volid, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volumeID %s: %v", vol.VolumeID(), err)
	}

	if volid == vmID {
		return nil
	}

	sourceNode, err := findVMNode(ctx, cl, volid)
	if err != nil {
		return fmt.Errorf("failed to find vm by id %d: %v", volid, err)
	}

	targetNode, err := findVMNode(ctx, cl, target)
	if err != nil {
		return fmt.Errorf("failed to find vm by id %d: %v", target, err)
	}

	if sourceNode == targetNode {
		return nil
	}

	upid, err := cl.Nodes(sourceNode).Qemu().Migrate(ctx, volid, &qemu.MigrateOptions{
		Target: targetNode,
		Online: false,
	})
	if err != nil {
		return fmt.Errorf("failed to migrate vm config: %v", err)
	}

	if upid != "" {
		if err := cl.Nodes(sourceNode).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
			return fmt.Errorf("unable to migrate virtual machine: %w", err)
		}
	}

	return nil
}

// deleteVM stops (if running) and destroys the guest, waiting for both tasks.
func deleteVM(ctx context.Context, cl *proxmoxrest.Client, node string, vmid int) error {
	status, err := cl.Nodes(node).Qemu().Status(ctx, vmid)
	if err != nil {
		return fmt.Errorf("unable to find vm with id %d: %w", vmid, err)
	}

	if status.Status == qemu.VMStatusRunning {
		upid, err := cl.Nodes(node).Qemu().Stop(ctx, vmid, nil)
		if err != nil {
			return fmt.Errorf("failed to stop vm %d: %v", vmid, err)
		}

		if upid != "" {
			if err := cl.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: time.Minute}); err != nil {
				return fmt.Errorf("unable to stop vm %d: %w", vmid, err)
			}
		}
	}

	upid, err := cl.Nodes(node).Qemu().Delete(ctx, vmid, nil)
	if err != nil {
		return fmt.Errorf("cannot delete vm with id %d: %w", vmid, err)
	}

	if upid != "" {
		if err := cl.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: time.Minute}); err != nil {
			return fmt.Errorf("unable to delete vm %d: %w", vmid, err)
		}
	}

	return nil
}

func deleteReplication(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume, vmID int) error {
	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volumeID %s: %v", vol.VolumeID(), err)
	}

	if id == vmID {
		return nil
	}

	resources, err := cl.Cluster().Resources().List(ctx, pxcluster.ListFilter{
		Type:      pxcluster.ResourceTypeVM,
		GuestType: guestTypeQemu,
		VMID:      id,
		Match: func(rs *pxcluster.Resource) (bool, error) {
			return rs.Name == vol.PV(), nil
		},
	})
	if err != nil {
		return err
	}

	if len(resources) == 0 {
		return nil
	}

	vmr := resources[0]

	jobs, err := cl.Nodes(vmr.Node).Replication().List(ctx, vmr.VMID)
	if err != nil {
		return fmt.Errorf("could not get replication list: %w", err)
	}

	for _, job := range jobs {
		if err := cl.Cluster().Replication().Delete(ctx, job.ID, false, false); err != nil {
			if !proxmoxrest.IsNotFound(err) {
				return fmt.Errorf("failed to delete replication schedule: %v", err)
			}
		}
	}

	if err := deleteVM(ctx, cl, vmr.Node, vmr.VMID); err != nil {
		return fmt.Errorf("failed to delete replication vm: %v", err)
	}

	return nil
}

func createVolume(ctx context.Context, cl *proxmoxrest.Client, vol *volume.Volume, sizeBytes int64) error {
	if vol.Node() == "" {
		return errors.New("node is required")
	}

	filename := strings.Split(vol.Disk(), "/")

	id, err := strconv.Atoi(vol.VMID())
	if err != nil {
		return fmt.Errorf("failed to parse volume vm id: %v", err)
	}

	disk, err := cl.Nodes(vol.Node()).Storage().Content(vol.Storage()).Create(ctx, &storage.CreateVolumeOptions{
		Filename: filename[len(filename)-1],
		VMID:     id,
		Size:     strconv.FormatInt(sizeBytes/1024, 10),
	})
	if err != nil {
		return fmt.Errorf("failed to create vm disk: %v", err)
	}

	diskName := strings.Split(disk, ":")
	if len(diskName) > 1 {
		vol.SetDisk(diskName[1])
	}

	return nil
}

func attachVolume(ctx context.Context, cl *proxmoxrest.Client, id int, vol *volume.Volume, options map[string]string) (map[string]string, error) {
	node, err := findVMNode(ctx, cl, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get vm config: %v", err)
	}

	cfg, err := cl.Nodes(node).Qemu().Config(ctx, id, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get vm config: %v", err)
	}

	wwm := ""

	lun, exist := isVolumeAttached(cfg, vol.Disk())
	if exist {
		wwm = hex.EncodeToString(fmt.Appendf(nil, "PVC-ID%02d", lun))
	} else {
		for lun = 1; lun < 30; lun++ {
			if _, used := cfg.SCSI[lun]; used {
				continue
			}

			wwm = hex.EncodeToString(fmt.Appendf(nil, "PVC-ID%02d", lun))

			drive := driveOptions(qemu.Drive{}, options)
			drive.File = vol.Storage() + ":" + vol.Disk()
			drive.WWN = "0x" + wwm

			upid, err := cl.Nodes(node).Qemu().AttachDrive(ctx, id, &qemu.AttachDriveOptions{
				Drive:   deviceNamePrefix + strconv.Itoa(lun),
				Options: drive,
			})
			if err != nil {
				return nil, fmt.Errorf("unable to attach disk: %v, drive=%+v", err, drive)
			}

			if upid != "" {
				if err := cl.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
					return nil, fmt.Errorf("unable to attach virtual machine disk: %w", err)
				}
			}

			if err := waitAttachVolume(ctx, cl, id, vol); err != nil {
				return nil, err
			}

			break
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

func detachVolume(ctx context.Context, cl *proxmoxrest.Client, id int, vol *volume.Volume) error {
	node, err := findVMNode(ctx, cl, id)
	if err != nil {
		if errors.Is(err, errVirtualMachineNotFound) {
			return nil
		}

		return fmt.Errorf("failed to get vm config: %v", err)
	}

	cfg, err := cl.Nodes(node).Qemu().Config(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	if lun, ok := isVolumeAttached(cfg, vol.Disk()); ok {
		device := deviceNamePrefix + strconv.Itoa(lun)

		if err := cl.Nodes(node).Qemu().Unlink(ctx, id, &qemu.UnlinkOptions{IDList: []string{device}}); err != nil {
			return fmt.Errorf("failed to unlink disk: %v", err)
		}
	}

	return nil
}

func updateVolume(ctx context.Context, cl *proxmoxrest.Client, id int, vol *volume.Volume, options map[string]string) error {
	node, err := findVMNode(ctx, cl, id)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	cfg, err := cl.Nodes(node).Qemu().Config(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("failed to get vm config: %v", err)
	}

	lun, ok := isVolumeAttached(cfg, vol.Disk())
	if !ok {
		return fmt.Errorf("volume is not attached to VM %d", id)
	}

	drive := driveOptions(cfg.SCSI[lun], options)

	upid, err := cl.Nodes(node).Qemu().AttachDrive(ctx, id, &qemu.AttachDriveOptions{
		Drive:   deviceNamePrefix + strconv.Itoa(lun),
		Options: drive,
	})
	if err != nil {
		return fmt.Errorf("unable to update disk: %v, drive=%+v", err, drive)
	}

	if upid != "" {
		if err := cl.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
			return fmt.Errorf("unable to update virtual machine disk: %w", err)
		}
	}

	return nil
}

func copyVolume(ctx context.Context, cl *proxmoxrest.Client, srcVol *volume.Volume, destVol *volume.Volume) error {
	if srcVol.Node() == "" {
		return errors.New("node is required")
	}

	if strings.Contains(destVol.Disk(), ".qcow2") {
		return errors.New("volume disk must not be qcow2 format")
	}

	opts := &storage.CopyOptions{
		Target: destVol.Disk(),
	}

	if srcVol.Node() != destVol.Node() && destVol.Node() != "" {
		opts.TargetNode = destVol.Node()
	}

	upid, err := cl.Nodes(srcVol.Node()).Storage().Content(srcVol.Storage()).Copy(ctx, srcVol.Disk(), opts)
	if err != nil {
		return fmt.Errorf("failed to copy pvc: %v, opts=%+v", err, opts)
	}

	if upid == "" {
		return nil
	}

	if err := cl.Nodes(srcVol.Node()).Tasks().Wait(ctx, upid, &tasks.WaitOptions{PollInterval: 15 * time.Second, Timeout: 4 * time.Minute}); err != nil {
		if failed, ok := errors.AsType[*tasks.FailedError](err); ok {
			return fmt.Errorf("failed to copy disk, exit status: %s", failed.ExitStatus)
		}

		return fmt.Errorf("unable to copy virtual machine disk: %w", err)
	}

	return nil
}

func waitAttachVolume(ctx context.Context, cl *proxmoxrest.Client, id int, vol *volume.Volume) error {
	err := retry.Constant(TaskTimeout*time.Second, retry.WithUnits(TaskStatusCheckInterval*time.Second)).Retry(func() error {
		node, err := findVMNode(ctx, cl, id)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		cfg, err := cl.Nodes(node).Qemu().Config(ctx, id, nil)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		if _, ok := isVolumeAttached(cfg, vol.Disk()); ok {
			return nil
		}

		return retry.ExpectedError(fmt.Errorf("volume %s is not attached to VM %d", vol.VolumeID(), id))
	})
	if err != nil {
		if retry.IsTimeout(err) {
			return fmt.Errorf("volume %s is not attached to VM %d", vol.VolumeID(), id)
		}

		return err
	}

	return nil
}

func waitDetachVolume(ctx context.Context, cl *proxmoxrest.Client, id int, vol *volume.Volume) error {
	err := retry.Constant(TaskTimeout*time.Second, retry.WithUnits(TaskStatusCheckInterval*time.Second)).Retry(func() error {
		node, err := findVMNode(ctx, cl, id)
		if err != nil {
			if errors.Is(err, errVirtualMachineNotFound) {
				return nil
			}

			return fmt.Errorf("failed to get vm config: %v", err)
		}

		cfg, err := cl.Nodes(node).Qemu().Config(ctx, id, nil)
		if err != nil {
			return fmt.Errorf("failed to get vm config: %v", err)
		}

		if _, ok := isVolumeAttached(cfg, vol.Disk()); ok {
			return retry.ExpectedError(fmt.Errorf("volume %s still attached to VM %d", vol.VolumeID(), id))
		}

		return nil
	})
	if err != nil {
		if retry.IsTimeout(err) {
			return fmt.Errorf("volume %s still attached to VM %d", vol.VolumeID(), id)
		}

		return err
	}

	return nil
}

// resizeVMDisk grows a guest's disk, waiting for the resize task to complete.
func resizeVMDisk(ctx context.Context, cl *proxmoxrest.Client, node string, vmid int, disk, size string) error {
	upid, err := cl.Nodes(node).Qemu().Resize(ctx, vmid, &qemu.ResizeOptions{Disk: disk, Size: size})
	if err != nil {
		return fmt.Errorf("unable to resize virtual machine disk: %w", err)
	}

	if upid == "" {
		return nil
	}

	if err := cl.Nodes(node).Tasks().Wait(ctx, upid, &tasks.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
		return fmt.Errorf("unable to resize virtual machine disk: %w", err)
	}

	return nil
}

func defaultVMConfig() *qemu.Config {
	return &qemu.Config{
		Boot:    new("order=scsi0"),
		Agent:   &qemu.Agent{Enabled: new(false)},
		Machine: &qemu.Machine{Type: "pc"},
		Cores:   new(1),
		Memory:  &qemu.Memory{Current: new(512)},
		SCSIHW:  "virtio-scsi-single",
	}
}
