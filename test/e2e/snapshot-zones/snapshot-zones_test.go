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

package snapshotzones

import (
	"testing"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestVolumeSnapshotCrossZoneCopy covers scenario "Snapshot cross-zone copy"
// from docs/e2e.md: a VolumeSnapshotClass with parameters.zone set to a
// Proxmox zone distinct from the source volume's own copies the snapshot
// there instead of alongside the source (docs/volumesnapshot.md) - confirmed
// by parsing the zone back out of the resulting VolumeSnapshotContent's
// status.snapshotHandle (region/zone/storage/disk, see pkg/utils/volume),
// rather than just trusting the VolumeSnapshotClass parameter was honored.
//
// The target zone is auto-discovered from nodes' topology.kubernetes.io/zone
// labels (framework.ListZones) rather than requiring a developer to
// hand-configure one; E2E_SNAPSHOT_ZONE still works as an explicit override.
// Skipped (t.Skip) when the cluster doesn't advertise a second zone, since
// this scenario needs a real multi-zone test cluster the suite can't assume
// (see docs/e2e.md's Open questions). Split out from test/e2e/snapshot/ into
// its own scenario so that one stays focused on same-zone restore/clone.
func TestVolumeSnapshotCrossZoneCopy(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	const (
		stsName      = "snapshot-zones-source"
		snapshotName = "snapshot-zones-test"
		sourceSize   = "1Gi"
	)

	stsClient := f.Client.Clientset.AppsV1().StatefulSets(f.Namespace)
	snapshotClassName := f.Namespace + "-snapclass"

	// 0. Discover which Proxmox zones the cluster actually has, via node
	// topology labels. Skip fast, before creating anything, unless there's
	// at least two (or an explicit override) - this scenario needs a real
	// second zone.
	ctx, cancel := f.Context()
	zones, err := framework.ListZones(ctx, f.Client.Clientset)

	cancel()
	require.NoError(err, "failed to list node topology zones")

	if f.Config.SnapshotZone == "" && len(zones) < 2 {
		t.Skipf("cluster only advertises %d %s value(s) (%v) - need at least two to test cross-zone copy (set E2E_SNAPSHOT_ZONE to override)",
			len(zones), framework.TopologyZoneLabel, zones)
	}

	// 1. Create a single-replica StatefulSet as the snapshot source.
	f.Logf("creating statefulset %s (storageClass=%s size=%s replicas=1)", stsName, f.Config.StorageClass, sourceSize)

	sts := framework.NewTestStatefulSet(framework.StatefulSetOptions{
		Name:         stsName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.StorageClass,
		Replicas:     1,
		Size:         sourceSize,
	})

	ctx, cancel = f.Context()
	_, err = stsClient.Create(ctx, sts, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create statefulset")

	sourcePodName := stsName + "-0"

	done := f.Step("waiting for pod %s to be ready", sourcePodName)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, sourcePodName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", sourcePodName)

	sourcePVCName := framework.StatefulSetPVCName(stsName, 0)

	ctx, cancel = f.Context()
	_, sourcePV, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, sourcePVCName, f.Config.Timeout)

	cancel()
	require.NoError(err, "pvc %s never became bound", sourcePVCName)
	require.Equal(proxmoxcsi.DriverName, sourcePV.Spec.CSI.Driver, "pv %s was not provisioned by this driver", sourcePV.Name)

	sourceZone, err := framework.VolumeZone(sourcePV.Spec.CSI.VolumeHandle)
	require.NoError(err, "failed to parse zone from pv %s volume handle %q", sourcePV.Name, sourcePV.Spec.CSI.VolumeHandle)

	// Pick the target zone: an explicit E2E_SNAPSHOT_ZONE override, or the
	// first discovered zone distinct from the source's.
	targetZone := f.Config.SnapshotZone
	if targetZone == "" {
		for _, zone := range zones {
			if zone != sourceZone {
				targetZone = zone

				break
			}
		}
	}

	require.NotEmpty(targetZone, "no %s distinct from the source volume's zone %s was found among %v (set E2E_SNAPSHOT_ZONE to override)",
		framework.TopologyZoneLabel, sourceZone, zones)
	require.NotEqual(sourceZone, targetZone,
		"target zone %s must differ from the source volume's own zone %s for this check to be meaningful (set E2E_SNAPSHOT_ZONE to override)", targetZone, sourceZone)
	f.Logf("source pvc %s is in zone %s, targeting zone %s for the snapshot copy", sourcePVCName, sourceZone, targetZone)

	// 2. Create a throwaway VolumeSnapshotClass targeting the discovered
	// zone, and snapshot the source PVC through it.
	f.Logf("creating volumesnapshotclass %s (driver=%s zone=%s)", snapshotClassName, proxmoxcsi.DriverName, targetZone)

	vsc := framework.NewVolumeSnapshotClass(snapshotClassName, proxmoxcsi.DriverName, map[string]string{"zone": targetZone})

	ctx, cancel = f.Context()
	_, err = f.Client.Dynamic.Resource(framework.VolumeSnapshotClassGVR).Create(ctx, vsc, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create volumesnapshotclass %s", snapshotClassName)

	t.Cleanup(func() {
		f.Logf("deleting volumesnapshotclass %s", snapshotClassName)

		ctx, cancel := f.Context()
		defer cancel()

		if err := f.Client.Dynamic.Resource(framework.VolumeSnapshotClassGVR).Delete(ctx, snapshotClassName, metav1.DeleteOptions{}); err != nil {
			f.Logf("failed to delete volumesnapshotclass %s: %v", snapshotClassName, err)
		}
	})

	f.Logf("creating volumesnapshot %s (zone=%s) from pvc %s", snapshotName, targetZone, sourcePVCName)

	vs := framework.NewVolumeSnapshot(snapshotName, f.Namespace, snapshotClassName, sourcePVCName)

	ctx, cancel = f.Context()
	_, err = f.Client.Dynamic.Resource(framework.VolumeSnapshotGVR).Namespace(f.Namespace).Create(ctx, vs, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create volumesnapshot %s", snapshotName)

	done = f.Step("waiting for volumesnapshot %s to become ready", snapshotName)
	ctx, cancel = f.Context()
	snapStatus, err := framework.WaitForVolumeSnapshotReady(ctx, f.Client.Dynamic, f.Namespace, snapshotName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "volumesnapshot %s never became ready", snapshotName)
	require.NotEmpty(snapStatus.BoundVolumeSnapshotContentName, "volumesnapshot %s has no bound volumesnapshotcontent", snapshotName)

	// 3. The actual assertion: the snapshot's handle encodes the target
	// zone, not the source's.
	ctx, cancel = f.Context()
	handle, err := framework.VolumeSnapshotContentHandle(ctx, f.Client.Dynamic, snapStatus.BoundVolumeSnapshotContentName)

	cancel()
	require.NoError(err, "failed to read snapshotHandle from volumesnapshotcontent %s", snapStatus.BoundVolumeSnapshotContentName)

	gotZone, err := framework.VolumeZone(handle)
	require.NoError(err, "failed to parse zone from volumesnapshotcontent %s handle %q", snapStatus.BoundVolumeSnapshotContentName, handle)
	require.Equal(targetZone, gotZone, "volumesnapshot %s landed in zone %s, not the target zone %s", snapshotName, gotZone, targetZone)
	f.Logf("volumesnapshot %s landed in zone %s as expected", snapshotName, gotZone)

	// 4. Delete the VolumeSnapshot and confirm its VolumeSnapshotContent is
	// actually gone.
	f.Logf("deleting volumesnapshot %s", snapshotName)

	ctx, cancel = f.Context()
	err = f.Client.Dynamic.Resource(framework.VolumeSnapshotGVR).Namespace(f.Namespace).Delete(ctx, snapshotName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete volumesnapshot %s", snapshotName)

	done = f.Step("waiting for volumesnapshotcontent %s to be gone", snapStatus.BoundVolumeSnapshotContentName)
	ctx, cancel = f.Context()
	err = framework.WaitForVolumeSnapshotContentGone(ctx, f.Client.Dynamic, snapStatus.BoundVolumeSnapshotContentName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "volumesnapshotcontent %s was not deleted - backing proxmox disk copy may be orphaned", snapStatus.BoundVolumeSnapshotContentName)
}
