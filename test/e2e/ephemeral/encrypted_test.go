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

package ephemeral

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	proxmoxcsi "github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	nodeStageSecretNameParam      = "csi.storage.k8s.io/node-stage-secret-name"
	nodeStageSecretNamespaceParam = "csi.storage.k8s.io/node-stage-secret-namespace"
)

// TestEncryptedEphemeralVolume covers scenario "Encrypted volumes"
// mount a CSI generic ephemeral volume from an
// encryption-enabled StorageClass, then prove - not just assume - that the
// driver actually LUKS-encrypted the backing device with the passphrase
// configured in the StorageClass's node-stage secret
func TestEncryptedEphemeralVolume(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	const (
		podName    = "encrypted"
		volumeName = "pvc"
		size       = "1Gi"
	)

	// 1. Pick a target node and resolve the passphrase the StorageClass is
	// actually configured with, straight from its node-stage secret - not
	// a value we assume in advance.
	ctx, cancel := f.Context()
	node, err := framework.SelectTargetNode(ctx, f.Client.Clientset, f.Config.NodeName)

	cancel()
	require.NoError(err, "failed to pick a target node")
	f.Logf("targeting node %s (storageClass=%s)", node.Name, f.Config.EncryptedStorageClass)

	ctx, cancel = f.Context()
	sc, err := f.Client.Clientset.StorageV1().StorageClasses().Get(ctx, f.Config.EncryptedStorageClass, metav1.GetOptions{})

	cancel()
	require.NoError(err, "failed to get storageclass %s", f.Config.EncryptedStorageClass)

	secretName := sc.Parameters[nodeStageSecretNameParam]
	secretNamespace := sc.Parameters[nodeStageSecretNamespaceParam]

	require.NotEmpty(secretName, "storageclass %s has no %s parameter - is it configured for encryption?", f.Config.EncryptedStorageClass, nodeStageSecretNameParam)
	require.NotEmpty(secretNamespace, "storageclass %s has no %s parameter", f.Config.EncryptedStorageClass, nodeStageSecretNamespaceParam)

	ctx, cancel = f.Context()
	passphrase, err := framework.ReadSecretValue(ctx, f.Client.Clientset, secretNamespace, secretName, proxmoxcsi.EncryptionPassphraseKey)

	cancel()
	require.NoError(err, "failed to read passphrase from secret %s/%s", secretNamespace, secretName)
	require.NotEmpty(passphrase, "secret %s/%s key %s is empty", secretNamespace, secretName, proxmoxcsi.EncryptionPassphraseKey)

	// 2. Find the CSI node-plugin pod colocated on that node: it has the
	// host device access (and the cryptsetup binary) needed to inspect
	// dm-crypt state that isn't visible from inside a workload pod.
	ctx, cancel = f.Context()
	nodePluginPod, err := framework.FindPodOnNode(ctx, f.Client.Clientset, f.Config.NodePluginNamespace, f.Config.NodePluginLabelSelector, node.Name)

	cancel()
	require.NoError(err, "failed to find the csi node-plugin pod on node %s", node.Name)
	f.Logf("using node-plugin pod %s/%s (container %s) to inspect host state", nodePluginPod.Namespace, nodePluginPod.Name, f.Config.NodePluginContainer)

	ctx, cancel = f.Context()
	mappersBefore, err := framework.ListEncryptedMappers(ctx, f.Client.RESTConfig, f.Client.Clientset, nodePluginPod.Namespace, nodePluginPod.Name, f.Config.NodePluginContainer)

	cancel()
	require.NoError(err, "failed to list existing dm-crypt mappers on node %s", node.Name)

	// 3. Create the pod with an ephemeral encrypted volume, pinned to that
	// node, and wait for it to be usable.
	f.Logf("creating pod %s with an ephemeral encrypted volume pinned to node %s", podName, node.Name)

	pod := framework.NewEphemeralPod(framework.EphemeralPodOptions{
		Name:         podName,
		Namespace:    f.Namespace,
		StorageClass: f.Config.EncryptedStorageClass,
		Size:         size,
		NodeName:     node.Name,
		VolumeName:   volumeName,
	})

	ctx, cancel = f.Context()
	_, err = f.Client.Clientset.CoreV1().Pods(f.Namespace).Create(ctx, pod, metav1.CreateOptions{})

	cancel()
	require.NoError(err, "failed to create pod %s", podName)

	done := f.Step("waiting for pod %s to be ready", podName)
	ctx, cancel = f.Context()
	_, err = framework.WaitForPodReady(ctx, f.Client.Clientset, f.Namespace, podName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pod %s never became ready", podName)

	f.Logf("writing a test file into the mounted volume")

	ctx, cancel = f.Context()
	_, _, err = framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, f.Namespace, podName, "alpine",
		[]string{"sh", "-c", "echo e2e-encrypted-volume > /mnt/e2e.txt"}, nil)

	cancel()
	require.NoError(err, "failed to write a test file inside pod %s", podName)

	// 4. Identify the dm-crypt mapper this volume created (diffed against
	// the pre-create snapshot, so it's unambiguous even on a busy node),
	// and derive the raw device it wraps.
	done = f.Step("waiting for a new dm-crypt mapper to appear on node %s", node.Name)
	ctx, cancel = f.Context()
	mapper, err := framework.WaitForNewEncryptedMapper(ctx, f.Client.RESTConfig, f.Client.Clientset, nodePluginPod.Namespace, nodePluginPod.Name, f.Config.NodePluginContainer,
		mappersBefore, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "no new dm-crypt mapper appeared on node %s for pod %s's volume", node.Name, podName)

	// 5. The actual assertion: the passphrase configured in the
	// StorageClass's secret is the one that opens this device's LUKS
	// header - not just that *some* passphrase was used.
	f.Logf("verifying the configured passphrase opens %s (cryptsetup luksOpen --test-passphrase)", mapper)

	ctx, cancel = f.Context()
	_, stderr, err := framework.ExecInPod(ctx, f.Client.RESTConfig, f.Client.Clientset, nodePluginPod.Namespace, nodePluginPod.Name, f.Config.NodePluginContainer,
		[]string{"cryptsetup", "luksOpen", "--test-passphrase", mapper, "--key-file=-"}, strings.NewReader(passphrase))

	cancel()
	require.NoError(err, "passphrase from secret %s/%s did not open %s (stderr: %s)", secretNamespace, secretName, mapper, stderr)

	// 6. Delete the pod and confirm both the auto-created PVC and its PV
	// are gone - unlike a StatefulSet's PVC, an ephemeral volume's PVC is
	// owned by the pod and must be cleaned up with it.
	pvcName := framework.EphemeralPVCName(podName, volumeName)

	ctx, cancel = f.Context()
	_, pv, err := framework.WaitForPVCBound(ctx, f.Client.Clientset, f.Namespace, pvcName, f.Config.Timeout)

	cancel()
	require.NoError(err, "pvc %s never became bound", pvcName)

	f.Logf("deleting pod %s", podName)

	ctx, cancel = f.Context()
	err = f.Client.Clientset.CoreV1().Pods(f.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})

	cancel()
	require.NoError(err, "failed to delete pod %s", podName)

	done = f.Step("waiting for pvc %s to be gone", pvcName)
	ctx, cancel = f.Context()
	err = framework.WaitForPVCGone(ctx, f.Client.Clientset, f.Namespace, pvcName, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pvc %s was not cleaned up after its pod was deleted", pvcName)

	done = f.Step("waiting for pv %s to be gone", pv.Name)
	ctx, cancel = f.Context()
	err = framework.WaitForPVGone(ctx, f.Client.Clientset, pv.Name, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "pv %s was not deleted after its pvc was removed - Proxmox disk may be orphaned", pv.Name)
}
