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

package rebind_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	k8stesting "k8s.io/client-go/testing"
)

// usedPair returns a bound claim protected by kubernetes.io/pvc-protection, as every claim in a
// cluster is, with its volume.
func usedPair(claim, volume string) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolume) {
	pvc, pv := boundPair(claim, volume, corev1.PersistentVolumeReclaimDelete)
	pvc.Finalizers = []string{pvcProtection}

	return pvc, pv
}

// pod returns a pod using the claim, scheduled on node unless node is empty, and controlled by
// the StatefulSet sts unless sts is empty.
func pod(name, claim, node, sts string) *corev1.Pod {
	p := &corev1.Pod{
		Name: name, Namespace: namespace, UID: uuid.NewUUID(),
		Spec: corev1.PodSpec{
			NodeName: node,
			Volumes: []corev1.Volume{{
				Name:                  "data",
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	if node == "" {
		p.Status.Phase = corev1.PodPending
	}

	if sts != "" {
		yes := true
		p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts, UID: uuid.NewUUID(), Controller: &yes}}
	}

	return p
}

func statefulSet(name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{Name: name, Namespace: namespace}
}

// newProtectedKube returns a fake cluster that simulates the pvc-protection controller.
func newProtectedKube(t *testing.T, objs ...runtime.Object) *fakeKube {
	t.Helper()

	kube := newFakeKube(t, objs...)
	kube.protection = true

	return kube
}

// recreatePodsOn makes every patch of a StatefulSet from the given attempt on create a pod
// using claim, as the StatefulSet controller would once it notices the claim is back. It
// returns the number of patches seen.
func recreatePodsOn(t *testing.T, kube *fakeKube, attempt int, claim string) *int {
	t.Helper()

	patches := 0

	kube.PrependReactor("patch", "statefulsets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patches++

		if attempt > 0 && patches >= attempt {
			p := pod("recreated", claim, "", action.(k8stesting.PatchAction).GetName()) //nolint: errcheck
			if err := kube.Tracker().Add(p); err != nil && !apierrors.IsAlreadyExists(err) {
				return true, nil, err
			}
		}

		return false, nil, nil
	})

	return &patches
}

func TestDrainUnused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	kube := newProtectedKube(t, pvc, pv)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	nodes, err := h.Drain(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, nodes)

	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}))
	assertRebound(t, kube, []want{{claim: "b", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}})

	// Nothing was recorded, so there is nothing to trigger.
	require.NoError(t, h.TriggerStatefulSets(ctx))
}

func TestDrainInUseWithoutForce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	kube := newProtectedKube(t, pvc, pv, pod("web-0", "a", "node1", "web"))

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	nodes, err := h.Drain(ctx, false)
	require.ErrorIs(t, err, rebind.ErrInUse)
	assert.Equal(t, []string{"node1"}, nodes)

	_, err = kube.CoreV1().Pods(namespace).Get(ctx, "web-0", metav1.GetOptions{})
	require.NoError(t, err, "the pod must not be deleted without force")

	live, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Contains(t, live.Finalizers, pvcProtection)
}

func TestDrainForce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	kube := newProtectedKube(t, pvc, pv, pod("web-0", "a", "node1", "web"), statefulSet("web"))

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	nodes, err := h.Drain(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"node1"}, nodes)

	_, err = kube.CoreV1().Pods(namespace).Get(ctx, "web-0", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err), "the pod must be deleted in force mode")

	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")}))

	patches := recreatePodsOn(t, kube, 2, "a")

	require.NoError(t, h.TriggerStatefulSets(ctx))
	assert.Equal(t, 2, *patches)

	sts, err := kube.AppsV1().StatefulSets(namespace).Get(ctx, "web", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, sts.Annotations[rebind.AnnReboundAt])
}

func TestDrainSkipsUnscheduledPods(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	pending := pod("web-0", "a", "", "web")
	kube := newProtectedKube(t, pvc, pv, pending, statefulSet("web"))

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	// A pod that is not scheduled yet cannot start with the held claim: nothing waits on it,
	// and it is not deleted even without force.
	nodes, err := h.Drain(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, nodes)

	require.NoError(t, kube.CoreV1().Pods(namespace).Delete(ctx, "web-0", metav1.DeleteOptions{}))
	require.NoError(t, h.Abort(ctx))

	// Its StatefulSet was recorded all the same, and is triggered after the Abort.
	patches := recreatePodsOn(t, kube, 1, "a")

	require.NoError(t, h.TriggerStatefulSets(ctx))
	assert.Equal(t, 1, *patches)
}

func TestDrainSkipsFinishedPods(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	done := pod("job-0", "a", "node1", "")
	done.Status.Phase = corev1.PodSucceeded
	kube := newProtectedKube(t, pvc, pv, done)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	nodes, err := h.Drain(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestDrainSwap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvcA, pvA := usedPair("a", "pv-a")
	pvcB, pvB := usedPair("b", "pv-b")
	kube := newProtectedKube(t, pvcA, pvA, pvcB, pvB,
		pod("web-0", "a", "node1", "web"), pod("db-0", "b", "node2", "db"), statefulSet("web"), statefulSet("db"))

	h, err := rebind.Hold(ctx, kube, namespace, "a", "b")
	require.NoError(t, err)

	nodes, err := h.Drain(ctx, true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"node1", "node2"}, nodes)

	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}, rebind.Target{Claim: "b", NewName: "a"}))

	// Both StatefulSets are triggered until each claim has a pod again.
	patched := map[string]bool{}

	kube.PrependReactor("patch", "statefulsets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		name := action.(k8stesting.PatchAction).GetName() //nolint: errcheck
		patched[name] = true

		claim := map[string]string{"web": "a", "db": "b"}[name]
		_ = kube.Tracker().Add(pod(name+"-0", claim, "", name)) //nolint: errcheck

		return false, nil, nil
	})

	require.NoError(t, h.TriggerStatefulSets(ctx))
	assert.Equal(t, map[string]bool{"web": true, "db": true}, patched)
}

func TestDrainReplacedClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	kube := newFakeKube(t, pvc, pv)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	// Someone removes the finalizers of the held claim, and creates another one with its name.
	require.NoError(t, kube.Tracker().Delete(pvcResource, namespace, "a"))

	other, _ := boundPair("a", "pv-other", corev1.PersistentVolumeReclaimDelete)
	require.NoError(t, kube.Tracker().Add(other))

	_, err = h.Drain(ctx, true)
	require.ErrorContains(t, err, "deleted or replaced")
}

func TestDrainHonorsContext(t *testing.T) {
	t.Parallel()

	pvc, pv := usedPair("a", "pv-a")
	// Without the simulated controller, pvc-protection is never released.
	kube := newFakeKube(t, pvc, pv)

	h, err := rebind.Hold(context.Background(), kube, namespace, "a")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = h.Drain(ctx, true)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTriggerGivesUp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	kube := newProtectedKube(t, pvc, pv, pod("web-0", "a", "node1", "web"), statefulSet("web"))

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	_, err = h.Drain(ctx, true)
	require.NoError(t, err)

	require.NoError(t, h.Abort(ctx))

	patches := recreatePodsOn(t, kube, 0, "a")

	err = h.TriggerStatefulSets(ctx)
	require.ErrorContains(t, err, "no pod was created for claims a")
	assert.Equal(t, rebind.TriggerAttempts, *patches)
}

func TestDrainLogs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := usedPair("a", "pv-a")
	kube := newProtectedKube(t, pvc, pv, pod("web-0", "a", "node1", "web"))

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	var logs []string

	h.Logf = func(format string, _ ...any) { logs = append(logs, format) }

	_, err = h.Drain(ctx, true)
	require.NoError(t, err)
	assert.Contains(t, logs, "terminating pod %s on node %s")
}
