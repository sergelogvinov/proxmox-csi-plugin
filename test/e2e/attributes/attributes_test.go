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

package attributes

import (
	"testing"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestVolumeAttributesClassModify covers VolumeAttributesClass (docs/volume-attributes.yaml):
// a bound PVC's mutable Proxmox disk parameters can be changed after
// provisioning by pointing spec.volumeAttributesClassName at a new class,
// without recreating the volume. ControllerModifyVolume requires the volume
// to already be published (attached to a VM), so this needs a running pod -
// unlike a StorageClass, it does not need to be pre-provisioned by the
// cluster operator: it's a throwaway parameter set owned by this test, so
// the test creates and tears down the VolumeAttributesClass objects itself.
func TestVolumeAttributesClassModify(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	const (
		stsName = "attributes"
		size    = "1Gi"
	)

	vacClient := f.Client.Clientset.StorageV1().VolumeAttributesClasses()

	// Two classes with distinct parameters (mirroring docs/volume-attributes.yaml's
	// "test"/"test-2" pair), so the test can assert an actual transition
	// between them, not just that a single class can be applied once.
	vacs := []struct {
		Name       string
		Parameters map[string]string
	}{
		{
			Name: f.Namespace + "-attrs-1",
			Parameters: map[string]string{
				"backup":   "true",
				"diskIOPS": "400",
				"diskMBps": "120",
			},
		},
		{
			Name: f.Namespace + "-attrs-2",
			Parameters: map[string]string{
				"backup":   "false",
				"diskIOPS": "500",
				"diskMBps": "200",
			},
		},
	}

	for _, vac := range vacs {
		f.Logf("creating volumeattributesclass %s (parameters=%v)", vac.Name, vac.Parameters)

		obj := framework.NewVolumeAttributesClass(vac.Name, proxmoxcsi.DriverName, vac.Parameters)

		ctx, cancel := f.Context()
		_, err := vacClient.Create(ctx, obj, metav1.CreateOptions{})

		cancel()
		require.NoError(err, "failed to create volumeattributesclass %s", vac.Name)

		name := vac.Name

		t.Cleanup(func() {
			f.Logf("deleting volumeattributesclass %s", name)

			ctx, cancel := f.Context()
			defer cancel()

			if err := vacClient.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
				f.Logf("failed to delete volumeattributesclass %s: %v", name, err)
			}
		})
	}

	// Create a single-replica StatefulSet so the PVC is bound to a pod -
	// ControllerModifyVolume refuses to modify a volume that isn't
	// currently published to a VM.
	f.Logf("creating statefulset %s (storageClass=%s size=%s replicas=1)", stsName, f.Config.StorageClass, size)

	sts := framework.NewTestStatefulSet(framework.StatefulSetOptions{
		Name:         stsName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.StorageClass,
		Replicas:     1,
		Size:         size,
	})

	ctx, cancel := f.Context()
	_, err := f.Client.Clientset.AppsV1().StatefulSets(f.Namespace).Create(ctx, sts, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create statefulset")

	pod0Name := stsName + "-0"

	done := f.Step("waiting for pod %s to be ready", pod0Name)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, pod0Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", pod0Name)

	pvc0Name := framework.StatefulSetPVCName(stsName, 0)
	pvcClient := f.Client.Clientset.CoreV1().PersistentVolumeClaims(f.Namespace)

	done = f.Step("waiting for pvc %s to bind", pvc0Name)
	ctx, cancel = f.Context()
	pvc0, pv0, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, pvc0Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pvc %s never became bound", pvc0Name)
	require.Equal(proxmoxcsi.DriverName, pv0.Spec.CSI.Driver, "pv %s was not provisioned by this driver", pv0.Name)
	require.Nil(pvc0.Status.CurrentVolumeAttributesClassName, "pvc %s already has a volumeattributesclass before the test set one", pvc0Name)

	// If E2E_PROXMOX_CONFIG is set, also confirm the change actually landed
	// on the Proxmox side: find the VM behind the Kubernetes node pod-0 is
	// running on, then the SCSI disk backing this PVC's volume, and compare
	// its raw options against what the applied VolumeAttributesClass should
	// have produced - not just trust that Kubernetes reports success.
	pxPool, err := framework.NewProxmoxPool(f.Config)
	require.NoError(err, "failed to build proxmox pool from %s", f.Config.ProxmoxConfig)

	var node0 *corev1.Node

	if pxPool != nil {
		ctx, cancel := f.Context()
		pod0, err := f.Client.Clientset.CoreV1().Pods(f.Namespace).Get(ctx, pod0Name, metav1.GetOptions{})

		cancel()
		require.NoError(err)

		ctx, cancel = f.Context()
		node0, err = f.Client.Clientset.CoreV1().Nodes().Get(ctx, pod0.Spec.NodeName, metav1.GetOptions{})

		cancel()
		require.NoError(err, "failed to get node %s", pod0.Spec.NodeName)
	} else {
		f.Logf("E2E_PROXMOX_CONFIG not set, skipping direct Proxmox-side verification")
	}

	// Apply each class in turn and confirm the PVC actually converges onto
	// it, rather than getting stuck Pending/Infeasible.
	for _, vac := range vacs {
		f.Logf("modifying pvc %s to volumeattributesclass %s", pvc0Name, vac.Name)

		ctx, cancel := f.Context()
		pvc, err := pvcClient.Get(ctx, pvc0Name, metav1.GetOptions{})

		cancel()
		require.NoError(err)

		className := vac.Name
		pvc.Spec.VolumeAttributesClassName = &className

		ctx, cancel = f.Context()
		_, err = pvcClient.Update(ctx, pvc, metav1.UpdateOptions{})

		cancel()
		require.NoError(err, "failed to patch pvc %s to volumeattributesclass %s", pvc0Name, vac.Name)

		done := f.Step("waiting for pvc %s to converge to volumeattributesclass %s", pvc0Name, vac.Name)
		ctx, cancel = f.Context()
		modified, err := framework.WaitForPVCVolumeAttributesClass(ctx, f.Client.Clientset, f.Namespace, pvc0Name, vac.Name, f.Config.Timeout)

		cancel()
		done()
		require.NoError(err, "pvc %s did not converge to volumeattributesclass %s", pvc0Name, vac.Name)
		require.NotNil(modified.Status.CurrentVolumeAttributesClassName)
		require.Equal(vac.Name, *modified.Status.CurrentVolumeAttributesClassName)
		require.Nil(modified.Status.ModifyVolumeStatus, "pvc %s still reports an in-flight modify after converging", pvc0Name)

		f.Logf("pvc %s now reports currentVolumeAttributesClassName=%s", pvc0Name, vac.Name)

		if pxPool == nil {
			continue
		}

		// Translate the VolumeAttributesClass parameters through the same
		// ExtractModifyVolumeParameters/ToCFG path ControllerModifyVolume
		// itself uses, so the expected Proxmox option keys (iops_rd/iops_wr,
		// mbps_rd/mbps_wr, backup="0"/"1", ...) can't drift from what the
		// driver actually sends.
		want, err := proxmoxcsi.ExtractModifyVolumeParameters(vac.Parameters)
		require.NoError(err, "failed to translate volumeattributesclass %s parameters", vac.Name)

		f.Logf("checking vm disk options for pvc %s on the proxmox side", pvc0Name)

		ctx, cancel = f.Context()
		gotOptions, err := framework.VolumeDiskOptions(ctx, pxPool, node0, pv0)

		cancel()
		require.NoError(err, "failed to read proxmox disk options for pvc %s", pvc0Name)

		for key, wantValue := range want.ToCFG() {
			require.Equal(wantValue, gotOptions[key],
				"vm disk option %s does not match volumeattributesclass %s after modifying pvc %s", key, vac.Name, pvc0Name)
		}

		f.Logf("vm disk options for pvc %s match volumeattributesclass %s: %v", pvc0Name, vac.Name, gotOptions)
	}
}
