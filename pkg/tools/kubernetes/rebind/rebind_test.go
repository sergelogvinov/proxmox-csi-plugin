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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

// scenario is a Rebind to run against a fresh cluster, with the state it must end in.
type scenario struct {
	name    string
	objects func() []runtime.Object
	claims  []string
	targets []rebind.Target
	want    []want
}

// want is a claim expected after the Rebind, and the volume it must be bound to.
type want struct {
	claim, volume, handle, zone string
	policy                      corev1.PersistentVolumeReclaimPolicy
}

func pairs(ps ...[2]string) func() []runtime.Object {
	return func() []runtime.Object {
		objs := make([]runtime.Object, 0, 2*len(ps))

		for _, p := range ps {
			pvc, pv := boundPair(p[0], p[1], corev1.PersistentVolumeReclaimDelete)
			objs = append(objs, pvc, pv)
		}

		return objs
	}
}

var scenarios = []scenario{
	{
		name:    "rename",
		objects: pairs([2]string{"a", "pv-a"}),
		claims:  []string{"a"},
		targets: []rebind.Target{{Claim: "a", NewName: "b"}},
		want:    []want{{claim: "b", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}},
	},
	{
		name:    "swap",
		objects: pairs([2]string{"a", "pv-a"}, [2]string{"b", "pv-b"}),
		claims:  []string{"a", "b"},
		targets: []rebind.Target{{Claim: "a", NewName: "b"}, {Claim: "b", NewName: "a"}},
		want: []want{
			{claim: "b", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete},
			{claim: "a", volume: "pv-b", handle: "region-1/node1/local-lvm/vm-9999-pv-b", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete},
		},
	},
	{
		name:    "topology",
		objects: pairs([2]string{"a", "pv-a"}),
		claims:  []string{"a"},
		targets: []rebind.Target{{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")}},
		want:    []want{{claim: "a", volume: "pv-a", handle: "region-1/node2/local-lvm/vm-9999-pv-a", zone: "node2", policy: corev1.PersistentVolumeReclaimDelete}},
	},
	{
		name:    "abort",
		objects: pairs([2]string{"a", "pv-a"}),
		claims:  []string{"a"},
		want:    []want{{claim: "a", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}},
	},
}

func TestRebind(t *testing.T) {
	t.Parallel()

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			kube := newFakeKube(t, s.objects()...)

			h, err := rebind.Hold(ctx, kube, namespace, s.claims...)
			require.NoError(t, err)

			require.NoError(t, finish(ctx, h, s))

			assert.Empty(t, kube.violations)
			assertRebound(t, kube, s.want)
		})
	}
}

// TestRebindFailures fails each write of every scenario in turn, once rejected and once applied
// with its response lost. Whatever fails, no volume may be deleted unless retained, and
// finishing the Rebind must reach the same end state: a failed Hold is retried, a failed
// Commit or Abort is called again on the same handle.
func TestRebindFailures(t *testing.T) {
	t.Parallel()

	for _, s := range scenarios {
		for _, lost := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/lost=%v", s.name, lost), func(t *testing.T) {
				t.Parallel()

				testFailures(t, s, lost)
			})
		}
	}
}

func testFailures(t *testing.T, s scenario, lost bool) {
	t.Helper()

	for failAt := 1; ; failAt++ {
		{
			ctx := context.Background()
			kube := newFakeKube(t, s.objects()...)
			kube.failAt = failAt
			kube.lost = lost

			h, err := rebind.Hold(ctx, kube, namespace, s.claims...)

			failed := err != nil
			if failed {
				kube.failAt = 0

				h, err = rebind.Hold(ctx, kube, namespace, s.claims...)
				require.NoError(t, err, "failAt=%d", failAt)
			}

			if err = finish(ctx, h, s); err != nil {
				failed = true
				kube.failAt = 0

				require.NoError(t, finish(ctx, h, s), "failAt=%d", failAt)
			}

			assert.Empty(t, kube.violations, "failAt=%d", failAt)
			assertRebound(t, kube, s.want)

			if !failed {
				break
			}
		}
	}
}

func finish(ctx context.Context, h *rebind.Rebind, s scenario) error {
	if s.targets == nil {
		return h.Abort(ctx)
	}

	return h.Commit(ctx, s.targets...)
}

func TestHoldState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimDelete)
	kube := newFakeKube(t, pvc, pv)

	_, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	live, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, pvc.UID, live.UID)
	assert.NotNil(t, live.DeletionTimestamp)
	assert.Contains(t, live.Finalizers, rebind.Finalizer)

	vol, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, corev1.PersistentVolumeReclaimRetain, vol.Spec.PersistentVolumeReclaimPolicy)
	assert.Equal(t, "Delete", vol.Annotations[rebind.AnnReclaimPolicy])

	// A second Hold adopts the first one.
	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)
	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}))
	assertRebound(t, kube, []want{{claim: "b", volume: "pv-a", handle: pv.Spec.CSI.VolumeHandle, zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}})
}

func TestHoldLegacyFinalizer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimRetain)
	now := metav1.Now()
	pvc.DeletionTimestamp = &now
	pvc.Finalizers = []string{rebind.LegacyFinalizer}
	pv.Annotations = map[string]string{rebind.AnnReclaimPolicy: "Delete"}

	kube := newFakeKube(t, pvc, pv)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)
	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")}))

	assertRebound(t, kube, []want{{claim: "a", volume: "pv-a", handle: "region-1/node2/local-lvm/vm-9999-pv-a", zone: "node2", policy: corev1.PersistentVolumeReclaimDelete}})
}

func TestHoldNotBound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimDelete)
	pv.Spec.ClaimRef.UID = "other"

	kube := newFakeKube(t, pvc, pv)

	_, err := rebind.Hold(ctx, kube, namespace, "a")
	require.ErrorIs(t, err, rebind.ErrNotBound)
	assert.Zero(t, kube.writes)
}

func TestHoldRollsBack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	objs := pairs([2]string{"a", "pv-a"}, [2]string{"b", "pv-b"})()
	kube := newFakeKube(t, objs...)
	// Writes 1-3 hold claim a, write 4 retains volume pv-b.
	kube.failAt = 4

	_, err := rebind.Hold(ctx, kube, namespace, "a", "b")
	require.Error(t, err)

	// Claim a was already Terminating and is recreated as it was.
	assertRebound(t, kube, []want{
		{claim: "a", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete},
	})

	// Claim b was never deleted and is left as it is.
	pvc, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "b", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, objs[2].(*corev1.PersistentVolumeClaim).UID, pvc.UID) //nolint: errcheck
	assert.Nil(t, pvc.DeletionTimestamp)
	assert.NotContains(t, pvc.Finalizers, rebind.Finalizer)

	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-b", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, corev1.PersistentVolumeReclaimDelete, pv.Spec.PersistentVolumeReclaimPolicy)
	assert.NotContains(t, pv.Annotations, rebind.AnnReclaimPolicy)
}

func TestInUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimDelete)
	pvc.Finalizers = []string{"kubernetes.io/pvc-protection"}

	kube := newFakeKube(t, pvc, pv)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	writes := kube.writes

	require.ErrorIs(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}), rebind.ErrInUse)
	require.ErrorIs(t, h.Abort(ctx), rebind.ErrInUse)
	assert.Equal(t, writes, kube.writes, "nothing is changed")

	// Once the pod is gone, Kubernetes drops pvc-protection and the Rebind can go on.
	live, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "a", metav1.GetOptions{})
	require.NoError(t, err)

	live.Finalizers = []string{rebind.Finalizer}
	_, err = kube.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, live, metav1.UpdateOptions{})
	require.NoError(t, err)

	require.NoError(t, h.Abort(ctx))
	assertRebound(t, kube, []want{{claim: "a", volume: "pv-a", handle: pv.Spec.CSI.VolumeHandle, zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}})
}

func TestNameTaken(t *testing.T) {
	t.Parallel()

	for name, target := range map[string]string{
		"existing claim": "c",
		"invalid name":   "Not_Valid",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			kube := newFakeKube(t, pairs([2]string{"a", "pv-a"}, [2]string{"c", "pv-c"})()...)

			h, err := rebind.Hold(ctx, kube, namespace, "a")
			require.NoError(t, err)

			writes := kube.writes

			require.ErrorIs(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: target}), rebind.ErrNameTaken)
			assert.Equal(t, writes, kube.writes, "nothing is changed")

			require.NoError(t, h.Abort(ctx))
		})
	}
}

func TestAbortAfterCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	kube.failAt = kube.writes + 1

	require.Error(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}))
	require.ErrorIs(t, h.Abort(ctx), rebind.ErrCommitted)
}

func TestManifests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	writes := kube.writes

	objs, err := h.Manifests(rebind.Target{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")})
	require.NoError(t, err)
	require.Len(t, objs, 2)
	assert.Equal(t, writes, kube.writes)

	pv := objs[1].(*corev1.PersistentVolume) //nolint: errcheck
	assert.Equal(t, "PersistentVolume", pv.Kind)
	assert.Empty(t, pv.UID)
	assert.Equal(t, "region-1/node2/local-lvm/vm-9999-pv-a", pv.Spec.CSI.VolumeHandle)
	assert.Equal(t, corev1.PersistentVolumeReclaimRetain, pv.Spec.PersistentVolumeReclaimPolicy)
	assert.Equal(t, "Delete", pv.Annotations[rebind.AnnReclaimPolicy])
	assert.Equal(t, "a", pv.Spec.ClaimRef.Name)

	pvc := objs[0].(*corev1.PersistentVolumeClaim) //nolint: errcheck
	assert.Equal(t, "PersistentVolumeClaim", pvc.Kind)
	assert.Empty(t, pvc.UID)
	assert.Nil(t, pvc.DeletionTimestamp)
	assert.NotContains(t, pvc.Finalizers, rebind.Finalizer)
}

// assertRebound checks that each wanted claim is bound to its volume, with no trace of the
// Rebind left on either.
func assertRebound(t *testing.T, kube *fakeKube, wants []want) {
	t.Helper()

	ctx := context.Background()

	for _, w := range wants {
		pvc, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, w.claim, metav1.GetOptions{})
		require.NoError(t, err, "claim %s", w.claim)

		assert.Equal(t, w.volume, pvc.Spec.VolumeName, "claim %s", w.claim)
		assert.Equal(t, corev1.ClaimBound, pvc.Status.Phase, "claim %s", w.claim)
		assert.Nil(t, pvc.DeletionTimestamp, "claim %s", w.claim)
		assert.NotContains(t, pvc.Finalizers, rebind.Finalizer, "claim %s", w.claim)
		assert.NotContains(t, pvc.Finalizers, rebind.LegacyFinalizer, "claim %s", w.claim)
		assert.Equal(t, "10Gi", pvc.Spec.Resources.Requests.Storage().String(), "claim %s", w.claim)

		pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, w.volume, metav1.GetOptions{})
		require.NoError(t, err, "volume %s", w.volume)

		assert.Equal(t, w.policy, pv.Spec.PersistentVolumeReclaimPolicy, "volume %s", w.volume)
		assert.NotContains(t, pv.Annotations, rebind.AnnReclaimPolicy, "volume %s", w.volume)
		assert.Equal(t, w.handle, pv.Spec.CSI.VolumeHandle, "volume %s", w.volume)
		assert.Equal(t, affinity(w.zone), pv.Spec.NodeAffinity, "volume %s", w.volume)
		require.NotNil(t, pv.Spec.ClaimRef, "volume %s", w.volume)
		assert.Equal(t, pvc.UID, pv.Spec.ClaimRef.UID, "volume %s", w.volume)
	}

	claims, err := kube.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	for _, pvc := range claims.Items {
		assert.Nil(t, pvc.DeletionTimestamp, "claim %s is left terminating", pvc.Name)
	}
}

func TestHoldExpanding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimDelete)
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("20Gi")

	kube := newFakeKube(t, pvc, pv)

	_, err := rebind.Hold(ctx, kube, namespace, "a")
	require.ErrorContains(t, err, "being expanded")
	assert.Zero(t, kube.writes)
}

// TestForeignClaim checks that a claim someone else created under the target name is never
// taken for one created by the Rebind, even when it already names the volume.
func TestForeignClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	foreign := &corev1.PersistentVolumeClaim{}
	foreign.Name = "b"
	foreign.Namespace = namespace
	foreign.Spec.VolumeName = "pv-a"

	_, err := kube.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, foreign, metav1.CreateOptions{})
	require.NoError(t, err)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	writes := kube.writes

	require.ErrorIs(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}), rebind.ErrNameTaken)
	assert.Equal(t, writes, kube.writes, "nothing is changed")
	require.NoError(t, h.Abort(ctx))
}

// TestReplacedVolume checks that a volume replaced under the same name while the claim is
// held is never changed, and Commit fails instead.
func TestReplacedVolume(t *testing.T) {
	t.Parallel()

	for name, target := range map[string]rebind.Target{
		"rename":   {Claim: "a", NewName: "b"},
		"topology": {Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

			h, err := rebind.Hold(ctx, kube, namespace, "a")
			require.NoError(t, err)

			require.NoError(t, kube.CoreV1().PersistentVolumes().Delete(ctx, "pv-a", metav1.DeleteOptions{}))

			_, other := boundPair("other", "pv-a", corev1.PersistentVolumeReclaimDelete)
			other.Spec.CSI.VolumeHandle = "region-1/node2/local-lvm/vm-9999-pv-a"
			other.Spec.NodeAffinity = affinity("node2")

			other, err = kube.CoreV1().PersistentVolumes().Create(ctx, other, metav1.CreateOptions{})
			require.NoError(t, err)

			require.Error(t, h.Commit(ctx, target))

			live, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, other.UID, live.UID)
			assert.Equal(t, "other", live.Spec.ClaimRef.Name)
			assert.Equal(t, corev1.PersistentVolumeReclaimDelete, live.Spec.PersistentVolumeReclaimPolicy)
		})
	}
}

// TestVolumeChangedBeforeDelete switches the volume back to Delete right before Commit deletes
// it. The delete must not go through for that version.
func TestVolumeChangedBeforeDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	changed := false

	kube.PrependReactor("delete", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		if changed {
			return false, nil, nil
		}

		changed = true

		obj, err := kube.Tracker().Get(pvResource, "", "pv-a")
		require.NoError(t, err)

		pv := obj.(*corev1.PersistentVolume) //nolint: errcheck
		pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
		pv.ResourceVersion = kube.nextVersion()

		require.NoError(t, kube.Tracker().Update(pvResource, pv, ""))

		return false, nil, nil
	})

	target := rebind.Target{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")}

	require.Error(t, h.Commit(ctx, target))
	assert.Empty(t, kube.violations)

	// Run again, Commit retains the volume once more before deleting it.
	require.NoError(t, h.Commit(ctx, target))
	assert.Empty(t, kube.violations)
	assertRebound(t, kube, []want{{claim: "a", volume: "pv-a", handle: target.VolumeHandle, zone: "node2", policy: corev1.PersistentVolumeReclaimDelete}})
}

// TestVolumeReplacedBeforePatch replaces the volume between Commit reading it and patching it.
// The patch must not change the new volume.
func TestVolumeReplacedBeforePatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	_, other := boundPair("other", "pv-a", corev1.PersistentVolumeReclaimDelete)
	replaced := false

	kube.PrependReactor("patch", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		if !replaced {
			replaced = true

			require.NoError(t, kube.Tracker().Delete(pvResource, "", "pv-a"))
			require.NoError(t, kube.Tracker().Add(other))
		}

		return false, nil, nil
	})

	require.Error(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}))

	live, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, other.UID, live.UID)
	assert.Equal(t, "other", live.Spec.ClaimRef.Name)
}

// TestForeignClaimBeforeCreate creates a claim under the target name right before Commit
// creates its own. The volume must not be reserved for, or bound to, that claim.
func TestForeignClaimBeforeCreate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	created := false

	kube.PrependReactor("create", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		if !created {
			created = true

			foreign := &corev1.PersistentVolumeClaim{}
			foreign.Name = "b"
			foreign.Namespace = namespace
			foreign.UID = "foreign"
			foreign.Spec.VolumeName = "pv-a"

			require.NoError(t, kube.Tracker().Add(foreign))
		}

		return false, nil, nil
	})

	require.ErrorIs(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}), rebind.ErrNameTaken)

	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, types.UID("foreign"), pv.Spec.ClaimRef.UID)
	assert.Equal(t, corev1.PersistentVolumeReclaimRetain, pv.Spec.PersistentVolumeReclaimPolicy)

	foreign, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "b", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, corev1.ClaimBound, foreign.Status.Phase)
}

// TestReplacementClaimDeleted fails a Swap after the first claim is created and its volume
// reserved for it, then deletes that claim. Running Commit again must reserve the volume for
// the claim it creates instead.
func TestReplacementClaimDeleted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"}, [2]string{"b", "pv-b"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a", "b")
	require.NoError(t, err)

	failed := false

	kube.PrependReactor("patch", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if !failed && action.(k8stesting.PatchAction).GetName() == "pv-b" { //nolint: errcheck
			failed = true

			return true, nil, fmt.Errorf("injected failure")
		}

		return false, nil, nil
	})

	targets := []rebind.Target{{Claim: "a", NewName: "b"}, {Claim: "b", NewName: "a"}}

	require.Error(t, h.Commit(ctx, targets...))

	first, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "b", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.ClaimBound, first.Status.Phase)

	// The batch failed, so the volume of the deleted claim below must still be retained.
	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.PersistentVolumeReclaimRetain, pv.Spec.PersistentVolumeReclaimPolicy)
	require.NoError(t, kube.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, "b", metav1.DeleteOptions{}))

	require.NoError(t, h.Commit(ctx, targets...))
	assertRebound(t, kube, []want{
		{claim: "b", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete},
		{claim: "a", volume: "pv-b", handle: "region-1/node1/local-lvm/vm-9999-pv-b", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete},
	})
}

// TestHoldRollsBackAroundInUse fails holding claim b while claim a, already held, is in use.
// Claim a stays held, claim b must still be released.
func TestHoldRollsBackAroundInUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	objs := pairs([2]string{"a", "pv-a"}, [2]string{"b", "pv-b"})()
	objs[0].(*corev1.PersistentVolumeClaim).Finalizers = []string{"kubernetes.io/pvc-protection"} //nolint: errcheck

	kube := newFakeKube(t, objs...)
	// Writes 1-3 hold claim a, write 4 retains volume pv-b, write 5 adds the finalizer to b.
	kube.failAt = 5

	_, err := rebind.Hold(ctx, kube, namespace, "a", "b")
	require.ErrorIs(t, err, rebind.ErrInUse)

	a, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotNil(t, a.DeletionTimestamp)
	assert.True(t, rebind.IsHeld(a))

	b, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, "b", metav1.GetOptions{})
	require.NoError(t, err)
	assert.False(t, rebind.IsHeld(b))

	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-b", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, corev1.PersistentVolumeReclaimDelete, pv.Spec.PersistentVolumeReclaimPolicy)
	assert.NotContains(t, pv.Annotations, rebind.AnnReclaimPolicy)
}

func TestUnchangedTopologyKeepsVolume(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	objs := pairs([2]string{"a", "pv-a"})()
	kube := newFakeKube(t, objs...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)
	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", VolumeHandle: "region-1/node1/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node1")}))

	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, objs[1].(*corev1.PersistentVolume).UID, pv.UID, "the volume is not recreated") //nolint: errcheck
	assertRebound(t, kube, []want{{claim: "a", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}})
}

// TestHoldResumeReplacedVolume resumes a hold whose volume was replaced under the same name.
func TestHoldResumeReplacedVolume(t *testing.T) {
	t.Parallel()

	for name, recorded := range map[string]bool{"recorded": true, "legacy": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimRetain)
			now := metav1.Now()
			pvc.DeletionTimestamp = &now
			pvc.Finalizers = []string{rebind.Finalizer}

			if recorded {
				pvc.Annotations["csi.proxmox.sinextra.dev/held-volume"] = string(pv.UID)
			}

			// Each case changes only what its own check looks at.
			if recorded {
				pv.UID = "replacement"
			} else {
				pv.Spec.ClaimRef.UID = "someone-else"
			}

			kube := newFakeKube(t, pvc, pv)

			_, err := rebind.Hold(ctx, kube, namespace, "a")
			require.ErrorIs(t, err, rebind.ErrNotBound)
			assert.Zero(t, kube.writes)
		})
	}
}

// TestRetainKeepsNewAnnotations adds an annotation to a volume without any right before Hold
// retains it. The annotation must survive.
func TestRetainKeepsNewAnnotations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	added := false

	kube.PrependReactor("patch", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		if !added {
			added = true

			obj, err := kube.Tracker().Get(pvResource, "", "pv-a")
			require.NoError(t, err)

			pv := obj.(*corev1.PersistentVolume) //nolint: errcheck
			pv.Annotations = map[string]string{"example.com/other": "yes"}
			pv.ResourceVersion = kube.nextVersion()

			require.NoError(t, kube.Tracker().Update(pvResource, pv, ""))
		}

		return false, nil, nil
	})

	_, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "yes", pv.Annotations["example.com/other"])
	assert.Equal(t, "Delete", pv.Annotations[rebind.AnnReclaimPolicy])
}

func TestHoldExpandingWhileHeld(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pvc, pv := boundPair("a", "pv-a", corev1.PersistentVolumeReclaimRetain)
	now := metav1.Now()
	pvc.DeletionTimestamp = &now
	pvc.Finalizers = []string{rebind.LegacyFinalizer}
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("20Gi")

	kube := newFakeKube(t, pvc, pv)

	_, err := rebind.Hold(ctx, kube, namespace, "a")
	require.ErrorContains(t, err, "being expanded")
	assert.Zero(t, kube.writes)
}

// TestRetainKeepsOperatorPolicy switches the volume to Retain right before Hold retains it.
// The policy restored at the end must be that one, not the one read before.
func TestRetainKeepsOperatorPolicy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	changed := false

	kube.PrependReactor("patch", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		if !changed {
			changed = true

			obj, err := kube.Tracker().Get(pvResource, "", "pv-a")
			require.NoError(t, err)

			pv := obj.(*corev1.PersistentVolume) //nolint: errcheck
			pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
			pv.ResourceVersion = kube.nextVersion()

			require.NoError(t, kube.Tracker().Update(pvResource, pv, ""))
		}

		return false, nil, nil
	})

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)
	require.NoError(t, h.Commit(ctx, rebind.Target{Claim: "a", NewName: "b"}))

	assertRebound(t, kube, []want{{claim: "b", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimRetain}})
}

// TestAbortAroundForeignClaim lets a foreign claim take one name while Abort recreates two
// claims. The other claim must still be recreated.
func TestAbortAroundForeignClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"}, [2]string{"b", "pv-b"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a", "b")
	require.NoError(t, err)

	added := false

	kube.PrependReactor("create", "persistentvolumeclaims", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if !added && action.(k8stesting.CreateAction).GetObject().(*corev1.PersistentVolumeClaim).Name == "a" { //nolint: errcheck
			added = true

			foreign := &corev1.PersistentVolumeClaim{}
			foreign.Name = "a"
			foreign.Namespace = namespace
			foreign.UID = "foreign"

			require.NoError(t, kube.Tracker().Add(foreign))
		}

		return false, nil, nil
	})

	require.ErrorIs(t, h.Abort(ctx), rebind.ErrNameTaken)
	assertRebound(t, kube, []want{{claim: "b", volume: "pv-b", handle: "region-1/node1/local-lvm/vm-9999-pv-b", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}})

	pv, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, corev1.PersistentVolumeReclaimRetain, pv.Spec.PersistentVolumeReclaimPolicy)
	assert.NotEqual(t, types.UID("foreign"), pv.Spec.ClaimRef.UID)
}

// TestClaimDeletedBeforeVolume deletes the recreated claim while Commit waits for the old
// volume to go. The new volume must not be created for it, and Commit must fail.
func TestClaimDeletedBeforeVolume(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	deleted := false

	kube.PrependReactor("get", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		obj, err := kube.Tracker().Get(pvcResource, namespace, "a")
		if !deleted && err == nil && obj.(*corev1.PersistentVolumeClaim).Annotations[rebind.AnnRebindFrom] != "" { //nolint: errcheck
			deleted = true

			require.NoError(t, kube.Tracker().Delete(pvcResource, namespace, "a"))
		}

		return false, nil, nil
	})

	target := rebind.Target{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")}

	require.ErrorContains(t, h.Commit(ctx, target), "deleted or replaced")
	require.True(t, deleted)

	_, err = kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "no volume is created for the deleted claim")
	assert.Empty(t, kube.violations)

	// Running Commit again recreates the claim and its volume.
	require.NoError(t, h.Commit(ctx, target))
	assertRebound(t, kube, []want{{claim: "a", volume: "pv-a", handle: target.VolumeHandle, zone: "node2", policy: corev1.PersistentVolumeReclaimDelete}})
}

// TestRollbackSeesLateDelete lets the claim delete of a failed Hold take effect just before
// the rollback releases the claim in place. The claim must be recreated, never left to go
// away with its volume switched back to Delete.
func TestRollbackSeesLateDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)
	// Writes 1-2 retain the volume and add the finalizer, write 3 deletes the claim.
	kube.failAt = 3

	updates := 0

	kube.PrependReactor("update", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++

		// The second update is the rollback's release: the delete lands right before it.
		if updates == 2 {
			obj, err := kube.Tracker().Get(pvcResource, namespace, "a")
			require.NoError(t, err)

			pvc := obj.(*corev1.PersistentVolumeClaim) //nolint: errcheck
			now := metav1.Now()
			pvc.DeletionTimestamp = &now
			pvc.ResourceVersion = kube.nextVersion()

			require.NoError(t, kube.Tracker().Update(pvcResource, pvc, namespace))
		}

		return false, nil, nil
	})

	_, err := rebind.Hold(ctx, kube, namespace, "a")
	require.Error(t, err)
	require.GreaterOrEqual(t, updates, 2)

	assert.Empty(t, kube.violations)
	assertRebound(t, kube, []want{{claim: "a", volume: "pv-a", handle: "region-1/node1/local-lvm/vm-9999-pv-a", zone: "node1", policy: corev1.PersistentVolumeReclaimDelete}})
}

// TestRecreatedVolumeBeingDeleted fails restoring the policy of a recreated volume, then
// starts deleting that volume. Running Commit again must not accept it.
func TestRecreatedVolumeBeingDeleted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kube := newFakeKube(t, pairs([2]string{"a", "pv-a"})()...)

	h, err := rebind.Hold(ctx, kube, namespace, "a")
	require.NoError(t, err)

	failed := false

	kube.PrependReactor("patch", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if !failed && strings.Contains(string(action.(k8stesting.PatchAction).GetPatch()), `"remove"`) { //nolint: errcheck
			failed = true

			return true, nil, fmt.Errorf("injected failure")
		}

		return false, nil, nil
	})

	target := rebind.Target{Claim: "a", VolumeHandle: "region-1/node2/local-lvm/vm-9999-pv-a", NodeAffinity: affinity("node2")}

	require.Error(t, h.Commit(ctx, target))
	require.True(t, failed)

	obj, err := kube.Tracker().Get(pvResource, "", "pv-a")
	require.NoError(t, err)

	pv := obj.(*corev1.PersistentVolume) //nolint: errcheck
	now := metav1.Now()
	pv.DeletionTimestamp = &now
	pv.Finalizers = []string{"kubernetes.io/pv-protection"}
	pv.ResourceVersion = kube.nextVersion()

	require.NoError(t, kube.Tracker().Update(pvResource, pv, ""))

	require.ErrorContains(t, h.Commit(ctx, target), "being deleted")

	live, err := kube.CoreV1().PersistentVolumes().Get(ctx, "pv-a", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, corev1.PersistentVolumeReclaimRetain, live.Spec.PersistentVolumeReclaimPolicy)
}
