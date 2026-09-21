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

package shared

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestSharedStorageTopology covers Proxmox shared storage (docs/install.md):
// a StorageClass backed by a Proxmox storage marked "Shared" and accessible
// from every node in the region should provision a PV whose nodeAffinity
// only requires topology.kubernetes.io/region - not
// topology.kubernetes.io/zone - since, unlike a local (ZFS/LVM) disk, the
// volume isn't pinned to a single Proxmox node
// (pkg/csi/controller.go's CreateVolume: storageConfig.Shared == 1 with no
// per-node restriction on the Proxmox storage builds a topology carrying
// only the region segment).
//
// It then exercises the practical payoff of that assertion: the pod is
// deleted and recreated pinned to a *different* zone within the same
// region, and must come back up mounting the same, already-bound PVC with
// its data intact - proving the lack of a zone nodeAffinity requirement is
// actually exploitable, not just a property of the PV object nobody relies
// on.
//
// Needs at least two zones in the same region - since a PV's nodeAffinity
// requires an exact region match, the migrated-to node must share the
// original's region - skipped (t.Skip) otherwise, via the same
// framework.ZonesInSameRegion discovery the snapshot-zones scenario uses.
func TestSharedStorageTopology(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	// Pick a reference node to determine which region to test in, then
	// discover the zones within that region - zones are only meaningfully
	// comparable within one region (each is a distinct Proxmox cluster).
	ctx, cancel := f.Context()
	refNode, err := framework.SelectTargetNode(ctx, f.Client.Clientset, "")

	cancel()
	require.NoError(err, "failed to find a reference node")

	region := refNode.Labels[corev1.LabelTopologyRegion]
	require.NotEmpty(region, "node %s has no %s label", refNode.Name, corev1.LabelTopologyRegion)

	ctx, cancel = f.Context()
	zones, err := framework.ZonesInSameRegion(ctx, f.Client.Clientset, region)

	cancel()
	require.NoError(err, "failed to list zones in region %s", region)

	if len(zones) < 2 {
		t.Skipf("region %s only advertises %d %s value(s) (%v) - need at least two to test pod migration across zones",
			region, len(zones), corev1.LabelTopologyZone, zones)
	}

	zoneA, zoneB := zones[0], zones[1]
	f.Logf("using region %s: zone %s for the initial pod, zone %s to migrate to", region, zoneA, zoneB)

	const (
		pvcName    = "shared"
		podName    = "shared"
		size       = "1Gi"
		dataFile   = "/mnt/data.txt"
		dataMarker = "e2e-shared"
	)

	pvcClient := f.Client.Clientset.CoreV1().PersistentVolumeClaims(f.Namespace)
	podClient := f.Client.Clientset.CoreV1().Pods(f.Namespace)

	// 1. Create the PVC and a pod pinned to zoneA.
	f.Logf("creating pvc %s (storageClass=%s size=%s)", pvcName, f.Config.SharedStorageClass, size)

	pvc := framework.NewPVC(framework.PVCOptions{
		Name:         pvcName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.SharedStorageClass,
		Size:         size,
	})

	ctx, cancel = f.Context()
	_, err = pvcClient.Create(ctx, pvc, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create pvc %s", pvcName)

	f.Logf("creating pod %s pinned to zone %s", podName, zoneA)

	pod := framework.NewPod(framework.PodOptions{
		Name:      podName,
		Namespace: f.Namespace,
		PVCName:   pvcName,
	})
	pod.Spec.NodeSelector = map[string]string{
		corev1.LabelTopologyRegion: region,
		corev1.LabelTopologyZone:   zoneA,
	}

	ctx, cancel = f.Context()
	_, err = podClient.Create(ctx, pod, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create pod %s", podName)

	done := f.Step("waiting for pod %s to be ready on zone %s", podName, zoneA)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, podName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", podName)

	ctx, cancel = f.Context()
	_, pv, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, pvcName, f.Config.Timeout)

	cancel()
	require.NoError(err, "pvc %s never became bound", pvcName)
	require.Equal(proxmoxcsi.DriverName, pv.Spec.CSI.Driver, "pv %s was not provisioned by this driver", pv.Name)

	// 2. The topology assertion: the PV's nodeAffinity requires the region
	// label and nothing else - in particular, no zone requirement that
	// would pin it to a single Proxmox node the way a local disk is.
	require.NotNil(pv.Spec.NodeAffinity, "pv %s has no nodeAffinity at all", pv.Name)
	require.NotNil(pv.Spec.NodeAffinity.Required, "pv %s nodeAffinity has no required terms", pv.Name)

	keys := map[string]bool{}

	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			keys[expr.Key] = true
		}
	}

	require.True(keys[corev1.LabelTopologyRegion], "pv %s nodeAffinity is missing a %s requirement", pv.Name, corev1.LabelTopologyRegion)
	require.False(keys[corev1.LabelTopologyZone],
		"pv %s nodeAffinity unexpectedly restricts %s - shared storage should be usable from any node in the region, not just the one it was provisioned on",
		pv.Name, corev1.LabelTopologyZone)
	f.Logf("pv %s nodeAffinity only requires %s, as expected for shared storage", pv.Name, corev1.LabelTopologyRegion)

	f.Logf("writing marker data into %s", dataFile)

	ctx, cancel = f.Context()
	_, _, err = framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, podName, "alpine",
		[]string{"sh", "-c", "echo " + dataMarker + " > " + dataFile}, nil)

	cancel()
	require.NoError(err, "failed to write marker data in pod %s", podName)

	// 3. Delete the pod (not the PVC) and recreate it pinned to zoneB - the
	// practical payoff: the shared PVC's nodeAffinity must not have pinned
	// it to zoneA.
	f.Logf("deleting pod %s", podName)

	ctx, cancel = f.Context()
	err = podClient.Delete(ctx, podName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete pod %s", podName)

	done = f.Step("waiting for pod %s to be deleted", podName)
	ctx, cancel = f.Context()
	err = framework.WaitForPodGone(ctx, f.Client.Clientset, f.Namespace, podName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s was not deleted", podName)

	f.Logf("recreating pod %s pinned to zone %s", podName, zoneB)

	pod = framework.NewPod(framework.PodOptions{
		Name:      podName,
		Namespace: f.Namespace,
		PVCName:   pvcName,
	})
	pod.Spec.NodeSelector = map[string]string{
		corev1.LabelTopologyRegion: region,
		corev1.LabelTopologyZone:   zoneB,
	}

	ctx, cancel = f.Context()
	_, err = podClient.Create(ctx, pod, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to recreate pod %s", podName)

	done = f.Step("waiting for pod %s to be ready on zone %s", podName, zoneB)
	ctx, cancel = f.Context()
	migratedPod, err := framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, podName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready again on zone %s - shared pvc %s did not follow it across zones", podName, zoneB, pvcName)

	// Belt-and-braces: the node it actually landed on really is in zoneB,
	// and satisfies the PV's nodeAffinity - catches an accidentally
	// empty/unsatisfiable requirement, which the key check above wouldn't.
	ctx, cancel = f.Context()
	migratedNode, err := f.Client.Clientset.CoreV1().Nodes().Get(ctx, migratedPod.Spec.NodeName, metav1.GetOptions{})

	cancel()
	require.NoError(err, "failed to get node %s", migratedPod.Spec.NodeName)
	require.Equal(zoneB, migratedNode.Labels[corev1.LabelTopologyZone], "pod %s landed on node %s, not in zone %s", podName, migratedNode.Name, zoneB)
	require.True(framework.NodeMatchesVolumeNodeAffinity(pv.Spec.NodeAffinity, migratedNode),
		"pv %s nodeAffinity does not match the labels of node %s in zone %s", pv.Name, migratedNode.Name, zoneB)

	f.Logf("verifying marker data survived the move to zone %s", zoneB)

	ctx, cancel = f.Context()
	out, _, err := framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, podName, "alpine",
		[]string{"cat", dataFile}, nil)

	cancel()
	require.NoError(err, "failed to read marker data from pod %s after migrating to zone %s", podName, zoneB)
	require.Equal(dataMarker, strings.TrimSpace(out), "data written before the pod migrated to zone %s did not survive", zoneB)

	// 4. Cleanup: delete the pod/PVC and confirm the PV is gone.
	f.Logf("deleting pod %s", podName)

	ctx, cancel = f.Context()
	err = podClient.Delete(ctx, podName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete pod %s", podName)

	ctx, cancel = f.Context()
	err = pvcClient.Delete(ctx, pvcName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete pvc %s", pvcName)

	done = f.Step("waiting for pv %s to be gone", pv.Name)
	ctx, cancel = f.Context()
	err = framework.WaitForPVGone(ctx, f.Client.Clientset, pv.Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pv %s was not deleted", pv.Name)
}
