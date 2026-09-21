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

package snapshot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestVolumeSnapshotRestore covers scenarios "Snapshot + restore" and "Clone
// from existing PVC" from docs/e2e.md: snapshot a single-replica
// StatefulSet's PVC (known data written into it), wait for the snapshot to
// become readyToUse, then restore it into two new standalone PVCs
// (dataSource: VolumeSnapshot) - one at the source size, one larger, per
// docs/volumesnapshot.md's note that a restore size must be >= the source's -
// and confirm the restored data's checksum matches the source's in both
// cases. Also clones a PVC directly from the source (dataSource:
// PersistentVolumeClaim, no snapshot involved) and confirms the same
// data-integrity check. Finally confirms a Delete VolumeSnapshotClass
// actually removes the VolumeSnapshotContent (and, transitively, its backing
// Proxmox disk copy) rather than leaving one orphaned.
func TestVolumeSnapshotRestore(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	const (
		stsName      = "snapshot-source"
		snapshotName = "snapshot-test"
		sourceSize   = "1Gi"
		largerSize   = "2Gi"
		dataFile     = "/mnt/data.bin"
	)

	stsClient := f.Client.Clientset.AppsV1().StatefulSets(f.Namespace)
	pvcClient := f.Client.Clientset.CoreV1().PersistentVolumeClaims(f.Namespace)
	podClient := f.Client.Clientset.CoreV1().Pods(f.Namespace)
	snapshotClassName := f.Namespace + "-snapclass"

	// 1. Create a single-replica StatefulSet as the snapshot source - same
	// builder the lifecycle/attributes scenarios use to get a PVC bound to a
	// running pod - and write known data into its volume.
	f.Logf("creating statefulset %s (storageClass=%s size=%s replicas=1)", stsName, f.Config.StorageClass, sourceSize)

	sts := framework.NewTestStatefulSet(framework.StatefulSetOptions{
		Name:         stsName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.StorageClass,
		Replicas:     1,
		Size:         sourceSize,
	})

	ctx, cancel := f.Context()
	_, err := stsClient.Create(ctx, sts, metav1.CreateOptions{})

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

	f.Logf("writing 4Mi of random data into %s and hashing it", dataFile)

	ctx, cancel = f.Context()
	_, _, err = framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, sourcePodName, "alpine",
		[]string{"sh", "-c", "dd if=/dev/urandom of=" + dataFile + " bs=1M count=4 2>/dev/null && sync"}, nil)

	cancel()
	require.NoError(err, "failed to write test data in pod %s", sourcePodName)

	sourceChecksum := checksumOf(t, f, sourcePodName, dataFile)
	f.Logf("source data checksum: %s", sourceChecksum)

	// 2. Create a throwaway VolumeSnapshotClass and snapshot the source PVC.
	f.Logf("creating volumesnapshotclass %s (driver=%s)", snapshotClassName, proxmoxcsi.DriverName)

	vsc := framework.NewVolumeSnapshotClass(snapshotClassName, proxmoxcsi.DriverName, nil)

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

	f.Logf("creating volumesnapshot %s from pvc %s", snapshotName, sourcePVCName)

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
	require.NotNil(snapStatus.RestoreSize, "volumesnapshot %s reports no status.restoreSize", snapshotName)
	require.True(snapStatus.RestoreSize.Cmp(resource.MustParse(sourceSize)) >= 0,
		"volumesnapshot %s restoreSize %s is smaller than the source pvc's %s", snapshotName, snapStatus.RestoreSize.String(), sourceSize)
	f.Logf("volumesnapshot %s ready, bound to volumesnapshotcontent %s, restoreSize=%s",
		snapshotName, snapStatus.BoundVolumeSnapshotContentName, snapStatus.RestoreSize.String())

	// 3. Restore the snapshot into two PVCs - one at the source size, one
	// larger - and confirm the data in each matches the source.
	for _, restore := range []struct {
		suffix string
		size   string
	}{
		{suffix: "same-size", size: sourceSize},
		{suffix: "larger", size: largerSize},
	} {
		restorePVCName := "snapshot-restore-" + restore.suffix
		restorePodName := "snapshot-restore-" + restore.suffix

		f.Logf("restoring volumesnapshot %s into pvc %s (size=%s)", snapshotName, restorePVCName, restore.size)

		restorePVC := framework.NewPVC(framework.PVCOptions{
			Name:         restorePVCName,
			Namespace:    f.Namespace,
			StorageClass: f.Config.StorageClass,
			Size:         restore.size,
			DataSource:   framework.NewVolumeSnapshotDataSource(snapshotName),
		})

		ctx, cancel := f.Context()
		_, err := pvcClient.Create(ctx, restorePVC, metav1.CreateOptions{})

		cancel()
		require.NoError(err, "failed to create restore pvc %s", restorePVCName)

		restorePod := framework.NewPod(framework.PodOptions{
			Name:      restorePodName,
			Namespace: f.Namespace,
			PVCName:   restorePVCName,
		})

		ctx, cancel = f.Context()
		_, err = podClient.Create(ctx, restorePod, metav1.CreateOptions{})

		cancel()
		require.NoError(err, "failed to create restore pod %s", restorePodName)

		done := f.Step("waiting for pod %s to be ready", restorePodName)
		ctx, cancel = f.Context()
		_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, restorePodName, f.Config.Timeout)

		cancel()
		done()
		require.NoError(err, "restore pod %s never became ready", restorePodName)

		ctx, cancel = f.Context()
		restorePVCBound, restorePV, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, restorePVCName, f.Config.Timeout)

		cancel()
		require.NoError(err, "restore pvc %s never became bound", restorePVCName)
		require.NotEqual(sourcePV.Name, restorePV.Name, "restore pvc %s was bound to the source pv instead of a new one", restorePVCName)

		wantSize := resource.MustParse(restore.size)
		require.Equal(wantSize.Value(), restorePVCBound.Status.Capacity.Storage().Value(),
			"restore pvc %s did not come up at the requested size %s", restorePVCName, restore.size)

		restoreChecksum := checksumOf(t, f, restorePodName, dataFile)
		require.Equal(sourceChecksum, restoreChecksum, "data restored into pvc %s does not match the source's checksum", restorePVCName)
		f.Logf("pvc %s (size=%s) restored with matching checksum %s", restorePVCName, restore.size, restoreChecksum)
	}

	// 4. Clone directly from the source PVC (dataSource: PersistentVolumeClaim,
	// not via a snapshot) and confirm the same data-integrity check.
	const (
		clonePVCName = "snapshot-clone"
		clonePodName = "snapshot-clone"
	)

	f.Logf("cloning pvc %s directly from pvc %s (size=%s)", clonePVCName, sourcePVCName, sourceSize)

	clonePVC := framework.NewPVC(framework.PVCOptions{
		Name:         clonePVCName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.StorageClass,
		Size:         sourceSize,
		DataSource:   framework.NewPVCCloneDataSource(sourcePVCName),
	})

	ctx, cancel = f.Context()
	_, err = pvcClient.Create(ctx, clonePVC, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create clone pvc %s", clonePVCName)

	clonePod := framework.NewPod(framework.PodOptions{
		Name:      clonePodName,
		Namespace: f.Namespace,
		PVCName:   clonePVCName,
	})

	ctx, cancel = f.Context()
	_, err = podClient.Create(ctx, clonePod, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create clone pod %s", clonePodName)

	done = f.Step("waiting for pod %s to be ready", clonePodName)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, clonePodName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "clone pod %s never became ready", clonePodName)

	ctx, cancel = f.Context()
	_, clonePV, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, clonePVCName, f.Config.Timeout)

	cancel()
	require.NoError(err, "clone pvc %s never became bound", clonePVCName)
	require.NotEqual(sourcePV.Name, clonePV.Name, "clone pvc %s was bound to the source pv instead of a new one", clonePVCName)

	cloneChecksum := checksumOf(t, f, clonePodName, dataFile)
	require.Equal(sourceChecksum, cloneChecksum, "data cloned into pvc %s does not match the source's checksum", clonePVCName)
	f.Logf("pvc %s cloned directly from pvc %s with matching checksum %s", clonePVCName, sourcePVCName, cloneChecksum)

	// 5. Delete the VolumeSnapshot and confirm its VolumeSnapshotContent is
	// actually gone - the Delete deletionPolicy's promise that the backing
	// Proxmox disk copy is cleaned up, not just the Kubernetes object.
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

// checksumOf execs sha256sum against path inside podName and returns just
// the hex digest (sha256sum's output is "<digest>  <path>").
func checksumOf(t *testing.T, f *framework.Framework, podName, path string) string {
	t.Helper()

	ctx, cancel := f.Context()
	defer cancel()

	stdout, _, err := framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, podName, "alpine",
		[]string{"sha256sum", path}, nil)
	require.NoError(t, err, "failed to checksum %s in pod %s", path, podName)

	fields := strings.Fields(stdout)
	require.NotEmpty(t, fields, "sha256sum produced no output for %s in pod %s", path, podName)

	return fields[0]
}
