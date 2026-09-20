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

package lifecycle

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestStatefulSetLifecycle covers the baseline e2e scenario from docs/e2e.md:
// deploy a StatefulSet, wait for it to be healthy, scale it out, resize one
// of its volumes online, delete it, and confirm every PersistentVolume it
// provisioned is actually gone afterward.
func TestStatefulSetLifecycle(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	const (
		stsName     = "lifecycle"
		initialSize = "1Gi"
		resizedSize = "2Gi"
	)

	stsClient := f.Client.Clientset.AppsV1().StatefulSets(f.Namespace)
	pvcClient := f.Client.Clientset.CoreV1().PersistentVolumeClaims(f.Namespace)
	pvClient := f.Client.Clientset.CoreV1().PersistentVolumes()

	// 1. Create the StatefulSet with a single replica.
	f.Logf("creating statefulset %s (storageClass=%s size=%s replicas=1)", stsName, f.Config.StorageClass, initialSize)

	sts := framework.NewTestStatefulSet(framework.StatefulSetOptions{
		Name:         stsName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.StorageClass,
		Replicas:     1,
		Size:         initialSize,
	})

	ctx, cancel := f.Context()
	_, err := stsClient.Create(ctx, sts, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create statefulset")

	// 2. Wait for pod -0 to be healthy, and its PVC/PV to exist and be
	// provisioned by this driver.
	pod0Name := stsName + "-0"

	done := f.Step("waiting for pod %s to be ready", pod0Name)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, pod0Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", pod0Name)

	pvc0Name := framework.StatefulSetPVCName(stsName, 0)

	done = f.Step("waiting for pvc %s to bind", pvc0Name)
	ctx, cancel = f.Context()
	_, pv0, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, pvc0Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pvc %s never became bound", pvc0Name)
	require.Equal(proxmoxcsi.DriverName, pv0.Spec.CSI.Driver, "pv %s was not provisioned by this driver", pv0.Name)
	f.Logf("pvc %s bound to pv %s", pvc0Name, pv0.Name)

	// 3. Scale to 2 replicas and wait for the second pod/PVC/PV.
	ctx, cancel = f.Context()
	sts, err = stsClient.Get(ctx, stsName, metav1.GetOptions{})

	cancel()
	require.NoError(err)

	replicas := int32(2)
	sts.Spec.Replicas = &replicas

	f.Logf("scaling statefulset %s to %d replicas", stsName, replicas)

	ctx, cancel = f.Context()
	_, err = stsClient.Update(ctx, sts, metav1.UpdateOptions{})

	cancel()
	require.NoError(err, "failed to scale statefulset to %d replicas", replicas)

	pod1Name := stsName + "-1"
	pvc1Name := framework.StatefulSetPVCName(stsName, 1)

	// Topology / WaitForFirstConsumer: the StorageClass must defer binding
	// until a pod that actually needs the volume exists, so pvc-1 must
	// still be unbound right after it's created - before pod-1 has even
	// been scheduled to a node, let alone become ready.
	ctx, cancel = f.Context()
	sc, err := f.Client.Clientset.StorageV1().StorageClasses().Get(ctx, f.Config.StorageClass, metav1.GetOptions{})

	cancel()
	require.NoError(err, "failed to get storageclass %s", f.Config.StorageClass)
	require.NotNil(sc.VolumeBindingMode, "storageclass %s has no volumeBindingMode set", f.Config.StorageClass)
	require.Equal(storagev1.VolumeBindingWaitForFirstConsumer, *sc.VolumeBindingMode,
		"storageclass %s must use WaitForFirstConsumer for this check to be meaningful", f.Config.StorageClass)

	done = f.Step("waiting for pvc %s to be created", pvc1Name)
	ctx, cancel = f.Context()
	pvc1AtCreation, err := framework.WaitForPVCExists(ctx, f.Client.Clientset, f.Namespace, pvc1Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pvc %s was never created", pvc1Name)
	require.Empty(pvc1AtCreation.Spec.VolumeName,
		"pvc %s was already bound before pod %s was scheduled - WaitForFirstConsumer should defer binding until a consumer exists", pvc1Name, pod1Name)
	f.Logf("pvc %s correctly still unbound (phase=%s) before pod %s is scheduled", pvc1Name, pvc1AtCreation.Status.Phase, pod1Name)

	done = f.Step("waiting for pod %s to be ready", pod1Name)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, pod1Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", pod1Name)

	done = f.Step("waiting for pvc %s to bind", pvc1Name)
	ctx, cancel = f.Context()
	_, pv1, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, pvc1Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pvc %s never became bound", pvc1Name)
	require.NotEqual(pv0.Name, pv1.Name, "both replicas ended up bound to the same PV")
	f.Logf("pvc %s bound to pv %s", pvc1Name, pv1.Name)

	f.Logf("checking pod anti-affinity spread %s and %s across nodes", pod0Name, pod1Name)

	ctx, cancel = f.Context()
	pod0, err := f.Client.Clientset.CoreV1().Pods(f.Namespace).Get(ctx, pod0Name, metav1.GetOptions{})

	cancel()
	require.NoError(err)

	ctx, cancel = f.Context()
	pod1, err := f.Client.Clientset.CoreV1().Pods(f.Namespace).Get(ctx, pod1Name, metav1.GetOptions{})

	cancel()
	require.NoError(err)
	require.NotEqual(pod0.Spec.NodeName, pod1.Spec.NodeName, "pod anti-affinity did not spread replicas across nodes")
	f.Logf("%s is on node %s, %s is on node %s", pod0Name, pod0.Spec.NodeName, pod1Name, pod1.Spec.NodeName)

	// Topology, continued: now that pod-1 landed somewhere, pv1's
	// nodeAffinity (set by the driver at provision time) must actually
	// match the node the scheduler put pod-1 on - not just exist.
	ctx, cancel = f.Context()
	node1, err := f.Client.Clientset.CoreV1().Nodes().Get(ctx, pod1.Spec.NodeName, metav1.GetOptions{})

	cancel()
	require.NoError(err, "failed to get node %s", pod1.Spec.NodeName)
	require.True(framework.NodeMatchesVolumeNodeAffinity(pv1.Spec.NodeAffinity, node1),
		"pv %s nodeAffinity does not match the labels of node %s it was actually scheduled to", pv1.Name, node1.Name)
	f.Logf("pv %s nodeAffinity matches node %s", pv1.Name, node1.Name)

	// 4. Scale down to 1 replica then back up to 2: pod-1's PVC must
	// survive the scale down (StatefulSets don't cascade-delete PVCs for
	// removed ordinals by default), and the recreated pod-1 must reuse the
	// same PV and see the same data - not get a fresh volume.
	const scaleMarker = "e2e-scale-marker"

	f.Logf("writing a marker file into %s's volume before scaling down", pod1Name)

	ctx, cancel = f.Context()
	_, _, err = framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, pod1Name, "alpine",
		[]string{"sh", "-c", "echo " + scaleMarker + " > /mnt/scale-marker.txt"}, nil)

	cancel()
	require.NoError(err, "failed to write marker file in pod %s", pod1Name)

	ctx, cancel = f.Context()
	sts, err = stsClient.Get(ctx, stsName, metav1.GetOptions{})

	cancel()
	require.NoError(err)

	replicas = 1
	sts.Spec.Replicas = &replicas

	f.Logf("scaling statefulset %s down to %d replica", stsName, replicas)

	ctx, cancel = f.Context()
	_, err = stsClient.Update(ctx, sts, metav1.UpdateOptions{})

	cancel()
	require.NoError(err, "failed to scale statefulset down to %d replica", replicas)

	done = f.Step("waiting for pod %s to be deleted", pod1Name)
	ctx, cancel = f.Context()
	err = framework.WaitForPodGone(ctx, f.Client.Clientset, f.Namespace, pod1Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s was not deleted after scaling down", pod1Name)

	ctx, cancel = f.Context()
	pvc1AfterScaleDown, err := pvcClient.Get(ctx, pvc1Name, metav1.GetOptions{})

	cancel()
	require.NoError(err, "pvc %s was deleted when it should have survived the scale down", pvc1Name)
	require.Equal(pv1.Name, pvc1AfterScaleDown.Spec.VolumeName, "pvc %s is no longer bound to the same pv after scaling down", pvc1Name)
	f.Logf("pvc %s survived the scale down, still bound to pv %s", pvc1Name, pv1.Name)

	// The pod is gone and the VolumeAttachment is cleaned up on the
	// Kubernetes side almost immediately, but detaching the disk from the
	// Proxmox VM itself happens asynchronously on the hypervisor. Give it
	// a moment before scaling back up, so the recreated pod's attach
	// doesn't race an in-flight detach of the same disk.
	const detachSettleDelay = 10 * time.Second

	f.Logf("waiting %s for the disk to finish detaching from the Proxmox VM", detachSettleDelay)
	time.Sleep(detachSettleDelay)

	ctx, cancel = f.Context()
	sts, err = stsClient.Get(ctx, stsName, metav1.GetOptions{})

	cancel()
	require.NoError(err)

	replicas = 2
	sts.Spec.Replicas = &replicas

	f.Logf("scaling statefulset %s back up to %d replicas", stsName, replicas)

	ctx, cancel = f.Context()
	_, err = stsClient.Update(ctx, sts, metav1.UpdateOptions{})

	cancel()
	require.NoError(err, "failed to scale statefulset back up to %d replicas", replicas)

	done = f.Step("waiting for pod %s to be ready again", pod1Name)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, pod1Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready again after scaling back up", pod1Name)

	ctx, cancel = f.Context()
	pvc1AfterScaleUp, err := pvcClient.Get(ctx, pvc1Name, metav1.GetOptions{})

	cancel()
	require.NoError(err)
	require.Equal(pv1.Name, pvc1AfterScaleUp.Spec.VolumeName, "pod %s was provisioned a different pv instead of reusing %s after scale down/up", pod1Name, pv1.Name)
	f.Logf("pod %s reused the same pv %s after scale down/up", pod1Name, pv1.Name)

	f.Logf("verifying the marker file written before scale down survived")

	ctx, cancel = f.Context()
	scaleMarkerOut, _, err := framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, pod1Name, "alpine",
		[]string{"cat", "/mnt/scale-marker.txt"}, nil)

	cancel()
	require.NoError(err, "failed to read marker file from pod %s after scale down/up", pod1Name)
	require.Equal(scaleMarker, strings.TrimSpace(scaleMarkerOut), "data written before scale down did not survive scale down/up")

	// 5. Resize pod -0's volume upward while its pod is running (online
	// resize) and confirm the filesystem inside the pod actually grew.
	wantSize := resource.MustParse(resizedSize)

	ctx, cancel = f.Context()
	pvc0, err := pvcClient.Get(ctx, pvc0Name, metav1.GetOptions{})

	cancel()
	require.NoError(err)

	pvc0.Spec.Resources.Requests[corev1.ResourceStorage] = wantSize

	f.Logf("resizing pvc %s from %s to %s", pvc0Name, initialSize, resizedSize)

	ctx, cancel = f.Context()
	_, err = pvcClient.Update(ctx, pvc0, metav1.UpdateOptions{})

	cancel()
	require.NoError(err, "failed to patch pvc %s to %s", pvc0Name, resizedSize)

	done = f.Step("waiting for pvc %s to finish resizing to %s", pvc0Name, resizedSize)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPVCResized(ctx, f.Client.Clientset, f.Namespace, pvc0Name, wantSize, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pvc %s did not resize to %s", pvc0Name, resizedSize)

	f.Logf("verifying the filesystem inside %s actually grew (df /mnt)", pod0Name)

	ctx, cancel = f.Context()
	stdout, _, err := framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, pod0Name, "alpine", []string{"df", "-k", "/mnt"}, nil)

	cancel()
	require.NoError(err, "failed to exec df in pod %s", pod0Name)
	require.True(dfShowsAtLeast(t, stdout, resizedSize), "pod %s filesystem did not reflect the resize:\n%s", pod0Name, stdout)

	// 6. Delete the StatefulSet, then explicitly delete both PVCs
	// (StatefulSets don't cascade-delete their PVCs by default) and assert
	// every PV it provisioned is actually gone - the real thing under test.
	f.Logf("deleting statefulset %s", stsName)

	ctx, cancel = f.Context()
	err = stsClient.Delete(ctx, stsName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete statefulset")

	for _, pvcName := range []string{pvc0Name, pvc1Name} {
		f.Logf("deleting pvc %s", pvcName)

		ctx, cancel := f.Context()
		err = pvcClient.Delete(ctx, pvcName, metav1.DeleteOptions{})

		cancel()
		require.NoError(err, "failed to delete pvc %s", pvcName)
	}

	for _, pvcName := range []string{pvc0Name, pvc1Name} {
		done := f.Step("waiting for pvc %s to be gone", pvcName)
		ctx, cancel := f.Context()
		err = framework.WaitForPVCGone(ctx, f.Client.Clientset, f.Namespace, pvcName, f.Config.Timeout)

		cancel()
		done()
		require.NoError(err, "pvc %s was not cleaned up", pvcName)
	}

	for _, pvName := range []string{pv0.Name, pv1.Name} {
		done := f.Step("waiting for pv %s to be gone", pvName)
		ctx, cancel := f.Context()
		err = framework.WaitForPVGone(ctx, f.Client.Clientset, pvName, f.Config.Timeout)

		cancel()
		done()
		require.NoError(err, "pv %s was not deleted after its pvc was removed - Proxmox disk may be orphaned", pvName)
	}

	// Belt-and-braces: no PV in the cluster should still reference this
	// test's namespace/StatefulSet by volume handle prefix.
	f.Logf("double-checking no leftover PV still references this test's volumes")

	ctx, cancel = f.Context()
	pvList, err := pvClient.List(ctx, metav1.ListOptions{})

	cancel()
	require.NoError(err)

	for _, pv := range pvList.Items {
		if pv.Spec.CSI == nil {
			continue
		}

		require.False(pv.Name == pv0.Name || pv.Name == pv1.Name, "pv %s still present after deletion", pv.Name)
	}
}

// dfShowsAtLeast is a coarse sanity check that the `df -k` output for /mnt
// reports a size consistent with the resized PVC, not the original one.
// It doesn't need to be exact: it's here to catch a resize that updated the
// Kubernetes objects but never actually grew the filesystem on the node.
func dfShowsAtLeast(t *testing.T, dfOutput, wantSize string) bool {
	t.Helper()

	want := resource.MustParse(wantSize)

	lines := strings.Split(strings.TrimSpace(dfOutput), "\n")
	if len(lines) < 2 {
		return false
	}

	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 2 {
		return false
	}

	totalSize, err := resource.ParseQuantity(fields[1] + "Ki")
	if err != nil {
		return false
	}

	// Filesystem overhead means df will report somewhat less than the raw
	// block size - allow it to be within 20% of the requested size rather
	// than requiring an exact match.
	return totalSize.Value() >= want.Value()*8/10
}
