//go:build e2e

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

package replication

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestZoneReplication covers Proxmox's ZFS zone-replication feature
// (docs/options.md's replicate/replicateZones StorageClass parameters): a
// PVC provisioned on a StorageClass with replicate: "true" is scheduled onto
// one of the class's declared replicateZones, and the driver
// (pkg/csi/utils.go's createReplication) wires up a Proxmox replication job
// keeping the disk in sync onto the other.
//
// The two zones are auto-discovered by reading the pre-existing
// StorageClass's own parameters.replicateZones rather than a separately
// hand-configured env var - that's the authoritative "where this class
// replicates" declaration (docs/options.md: "support up to 2 zones").
//
// Requires E2E_PROXMOX_CONFIG: the replication job Proxmox actually creates
// is not reflected on any Kubernetes object (PV, PVC, CSIStorageCapacity,
// ...), so without direct Proxmox API access there is nothing this suite
// can assert - skipped (t.Skip) otherwise.
func TestZoneReplication(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	pxPool, err := framework.NewProxmoxPool(f.Config)
	require.NoError(err, "failed to build proxmox pool from %s", f.Config.ProxmoxConfig)

	if pxPool == nil {
		t.Skip("E2E_PROXMOX_CONFIG not set - zone replication isn't visible from Kubernetes, so there is nothing to assert without direct Proxmox API access")
	}

	const (
		stsName = "zonereplication"
		size    = "1Gi"
	)

	stsClient := f.Client.Clientset.AppsV1().StatefulSets(f.Namespace)
	pvcClient := f.Client.Clientset.CoreV1().PersistentVolumeClaims(f.Namespace)

	// 1. Read the replicated StorageClass's own declared zones.
	ctx, cancel := f.Context()
	sc, err := f.Client.Clientset.StorageV1().StorageClasses().Get(ctx, f.Config.ReplicatedStorageClass, metav1.GetOptions{})

	cancel()
	require.NoError(err, "failed to get storageclass %s", f.Config.ReplicatedStorageClass)
	require.Equal("true", sc.Parameters["replicate"],
		"storageclass %s has no parameters.replicate=\"true\" - is it configured for zone replication? (see docs/options.md)", f.Config.ReplicatedStorageClass)

	zones := strings.Split(sc.Parameters["replicateZones"], ",")
	for i := range zones {
		zones[i] = strings.TrimSpace(zones[i])
	}

	require.Len(zones, 2, "storageclass %s parameters.replicateZones must list exactly 2 zones, got %q",
		f.Config.ReplicatedStorageClass, sc.Parameters["replicateZones"])
	require.NotEqual(zones[0], zones[1],
		"storageclass %s parameters.replicateZones must list two distinct zones", f.Config.ReplicatedStorageClass)
	f.Logf("storageclass %s replicates between zones %v", f.Config.ReplicatedStorageClass, zones)

	// 2. Provision a PVC on it and see which of the two zones it landed in.
	f.Logf("creating statefulset %s (storageClass=%s size=%s replicas=1)", stsName, f.Config.ReplicatedStorageClass, size)

	sts := framework.NewTestStatefulSet(framework.StatefulSetOptions{
		Name:         stsName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.ReplicatedStorageClass,
		Replicas:     1,
		Size:         size,
	})

	ctx, cancel = f.Context()
	_, err = stsClient.Create(ctx, sts, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create statefulset")

	podName := stsName + "-0"

	done := f.Step("waiting for pod %s to be ready", podName)
	ctx, cancel = f.Context()
	pod, err := framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, podName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", podName)

	// The volume handle can't tell us the zone here: createReplication
	// (pkg/csi/controller.go) hands out vol.VolumeSharedID() for replicated
	// volumes, which drops the zone segment because the PV's nodeAffinity
	// spans both of the storageclass's replicateZones. The pod's actual
	// node - where the driver placed the disk - is the only place the
	// source zone is still visible from Kubernetes.
	ctx, cancel = f.Context()
	node, err := f.Client.Clientset.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})

	cancel()
	require.NoError(err, "failed to get node %s", pod.Spec.NodeName)

	sourceZone := node.Labels[corev1.LabelTopologyZone]
	require.NotEmpty(sourceZone, "node %s has no %s label", node.Name, corev1.LabelTopologyZone)
	require.Contains(zones, sourceZone,
		"pod %s landed on node %s in zone %s, not one of storageclass %s's declared replicateZones %v", podName, node.Name, sourceZone, f.Config.ReplicatedStorageClass, zones)

	pvcName := framework.StatefulSetPVCName(stsName, 0)

	ctx, cancel = f.Context()
	_, pv, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, pvcName, f.Config.Timeout)

	cancel()
	require.NoError(err, "pvc %s never became bound", pvcName)
	require.Equal(proxmoxcsi.DriverName, pv.Spec.CSI.Driver, "pv %s was not provisioned by this driver", pv.Name)

	pvZones, err := framework.VolumeZone(pv.Spec.CSI.VolumeHandle)
	require.NoError(err, "failed to parse zone from pv %s volume handle %q", pv.Name, pv.Spec.CSI.VolumeHandle)
	require.Empty(pvZones,
		"pv %s landed in zone %s, should not have any zone information for replicated volumes", pv.Name, pvZones, f.Config.ReplicatedStorageClass, zones)

	targetZone := zones[0]
	if targetZone == sourceZone {
		targetZone = zones[1]
	}

	f.Logf("pvc %s provisioned in zone %s, expecting a replication job targeting zone %s", pvcName, sourceZone, targetZone)

	// 3. The actual assertion: Proxmox created a replication job for this
	// VM targeting the other zone - otherwise invisible from Kubernetes.
	// The job's Guest is the shadow VM prepareReplication (pkg/csi/utils.go)
	// created for this PVC, not the k8s node's own VM - its ID is embedded
	// in the volume handle's disk name (vm-<id>-<pvc>, see pkg/utils/volume).
	vol, err := volume.NewVolumeFromVolumeID(pv.Spec.CSI.VolumeHandle)
	require.NoError(err, "failed to parse pv %s volume handle %q", pv.Name, pv.Spec.CSI.VolumeHandle)

	vmID, err := strconv.Atoi(vol.VMID())
	require.NoError(err, "failed to parse shadow vm id from pv %s volume handle %q", pv.Name, pv.Spec.CSI.VolumeHandle)

	cl, err := pxPool.Get(vol.Region())
	require.NoError(err, "failed to get proxmox cluster client for region %s", vol.Region())

	done = f.Step("waiting for a replication job for vm %d targeting zone %s", vmID, targetZone)
	ctx, cancel = f.Context()
	job, err := framework.WaitForReplicationJob(ctx, cl, vmID, targetZone, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "no replication job for vm %d targeting zone %s appeared", vmID, targetZone)
	f.Logf("replication job %s targets zone %s as configured", job.ID, job.Target)

	// 4. Delete the StatefulSet/PVC and confirm the PV is gone -
	// DeleteVolume's deleteReplication (pkg/csi/utils.go) tears down the
	// replication job and its shadow VM as part of that.
	f.Logf("deleting statefulset %s", stsName)

	ctx, cancel = f.Context()
	err = stsClient.Delete(ctx, stsName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete statefulset")

	ctx, cancel = f.Context()
	err = pvcClient.Delete(ctx, pvcName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete pvc %s", pvcName)

	done = f.Step("waiting for pv %s to be gone", pv.Name)
	ctx, cancel = f.Context()
	err = framework.WaitForPVGone(ctx, f.Client.Clientset, pv.Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pv %s was not deleted - proxmox disk/replication job may be orphaned", pv.Name)

	// 5. Confirm deleteReplication (pkg/csi/utils.go) actually tore down
	// the replication job and its shadow VM too, not just the Kubernetes
	// PV - neither is visible from any Kubernetes object, so this is the
	// only way to catch an orphaned job/VM left behind on delete.
	shadowVMID, err := strconv.Atoi(strings.SplitN(job.ID, "-", 2)[0])
	require.NoError(err, "failed to parse shadow vm id from replication job id %q", job.ID)

	done = f.Step("waiting for replication job %s to be gone", job.ID)
	ctx, cancel = f.Context()
	err = framework.WaitForReplicationJobGone(ctx, cl, job.ID, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "replication job %s was not deleted after pv %s was removed", job.ID, pv.Name)

	done = f.Step("waiting for shadow vm %d to be gone", shadowVMID)
	ctx, cancel = f.Context()
	err = framework.WaitForShadowVMGone(ctx, cl, shadowVMID, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "shadow vm %d was not deleted after pv %s was removed", shadowVMID, pv.Name)
}
