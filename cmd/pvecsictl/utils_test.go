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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func init() {
	l := log.New()
	l.SetOutput(io.Discard)

	logger = log.NewEntry(l)
}

// fakeHold returns the queued errors from Abort, one per call, then nil.
type fakeHold struct {
	errs  []error
	calls int
}

func (f *fakeHold) Abort(context.Context) error {
	f.calls++

	if len(f.errs) == 0 {
		return nil
	}

	err := f.errs[0]
	f.errs = f.errs[1:]

	return err
}

func TestAbortHeld(t *testing.T) {
	t.Parallel()

	transient := errors.New("conflict")

	tests := []struct {
		name      string
		errs      []error
		wantErr   error
		wantCalls int
	}{
		{name: "aborted", wantCalls: 1},
		{name: "retried once after a transient failure", errs: []error{transient}, wantCalls: 2},
		{name: "fails after the retry", errs: []error{transient, transient}, wantErr: transient, wantCalls: 2},
		{name: "in use is not retried", errs: []error{fmt.Errorf("claim a: %w", rebind.ErrInUse)}, wantErr: rebind.ErrInUse, wantCalls: 1},
		{name: "committed is not retried", errs: []error{rebind.ErrCommitted}, wantErr: rebind.ErrCommitted, wantCalls: 1},
		{
			name:      "in use mixed with another failure is retried",
			errs:      []error{errors.Join(fmt.Errorf("claim a: %w", rebind.ErrInUse), transient)},
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := &fakeHold{errs: tt.errs}

			err := abortHeld(t.Context(), held)

			if tt.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.wantErr)
			}

			assert.Equal(t, tt.wantCalls, held.calls)
		})
	}
}

func TestRecoverySteps(t *testing.T) {
	t.Parallel()

	swap := []heldClaimRef{
		{Name: "a", UID: "uid-a", Volume: "pv-a", NewName: "b"},
		{Name: "b", UID: "uid-b", Volume: "pv-b", NewName: "a"},
	}

	steps := strings.Join(recoverySteps("ns", swap), "\n")

	// Every original is released before any claim is created, as the names cross.
	releaseA := strings.Index(steps, "If claim a with uid uid-a is still Terminating")
	releaseB := strings.Index(steps, "If claim b with uid uid-b is still Terminating")
	create := strings.Index(steps, "create it from its manifest")

	assert.GreaterOrEqual(t, releaseA, 0)
	assert.GreaterOrEqual(t, releaseB, 0)
	assert.GreaterOrEqual(t, create, 0)
	assert.Less(t, releaseA, create)
	assert.Less(t, releaseB, create)

	// The hold finalizer of a claim a pod still uses is left alone.
	assert.Contains(t, steps, "once no pod uses it")

	// A claim that already exists is not the end: its volume still has to be reserved.
	assert.Contains(t, steps, "If claim b exists with the annotation "+rebind.AnnRebindFrom+"=uid-a, it was already recreated")
	assert.Contains(t, steps, "reserve persistentvolume pv-a for claim b, even if the claim already exists")
	assert.Contains(t, steps, "reserve persistentvolume pv-b for claim a, even if the claim already exists")
	assert.Contains(t, steps, rebind.AnnReclaimPolicy)
	assert.Contains(t, steps, "If claim a with uid uid-a is still Terminating: once no pod uses it, remove the "+rebind.Finalizer+" finalizer")

	// A claim that keeps its name and was never deleted only needs the finalizer removed.
	same := strings.Join(recoverySteps("ns", []heldClaimRef{{Name: "a", UID: "uid-a", Volume: "pv-a"}}), "\n")
	assert.Contains(t, same, "If claim a with uid uid-a exists and is not Terminating")
}

func TestOnlyInUse(t *testing.T) {
	t.Parallel()

	inUse := fmt.Errorf("claim a: %w", rebind.ErrInUse)
	other := errors.New("conflict")

	assert.True(t, onlyInUse(inUse))
	assert.True(t, onlyInUse(errors.Join(inUse, fmt.Errorf("claim b: %w", rebind.ErrInUse))))
	assert.True(t, onlyInUse(fmt.Errorf("hold: %w", errors.Join(inUse))))
	assert.False(t, onlyInUse(errors.Join(inUse, other)))
	assert.False(t, onlyInUse(fmt.Errorf("hold: %w; release: %w", other, inUse)))
	assert.False(t, onlyInUse(other))
	assert.False(t, onlyInUse(nil))
}

func TestMergeNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"p1", "p2", "p3"}, mergeNames([]string{"p1", "p2"}, "p2", "p3"))
	assert.Empty(t, mergeNames(nil))
}

const testNamespace = "default"

func init() {
	vacateInterval = time.Millisecond
	triggerInterval = time.Millisecond
}

// heldPVC returns a held claim: Terminating, and still protected by Kubernetes when protected.
func heldPVC(name string, protected bool) *corev1.PersistentVolumeClaim {
	now := metav1.Now()

	pvc := &corev1.PersistentVolumeClaim{
		Name:              name,
		Namespace:         testNamespace,
		UID:               types.UID("uid-" + name),
		DeletionTimestamp: &now,
		Finalizers:        []string{"csi.proxmox.sinextra.dev/rebind"},
	}

	if protected {
		pvc.Finalizers = append(pvc.Finalizers, pvcProtectionFinalizer)
	}

	return pvc
}

// livePVC returns a claim that is in place.
func livePVC(name string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{Name: name, Namespace: testNamespace, UID: types.UID("uid-" + name)}
}

// claimPod returns a pod on node that uses the claims, controlled by StatefulSet sts if set.
func claimPod(name, node, sts string, claims ...string) *corev1.Pod {
	pod := &corev1.Pod{
		Name: name, Namespace: testNamespace, UID: types.UID("uid-" + name),
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	for _, claim := range claims {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name:                  claim,
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim},
		})
	}

	if sts != "" {
		controller := true
		pod.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
			Name:       sts,
			Controller: &controller,
		}}
	}

	return pod
}

// unprotectOnPodDelete makes Kubernetes drop pvc-protection from a claim once no pod uses it
// anymore, as the pvc-protection controller does.
func unprotectOnPodDelete(kube *fake.Clientset) {
	kube.PrependReactor("delete", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		name := action.(clienttesting.DeleteAction).GetName()
		gvr := corev1.SchemeGroupVersion.WithResource("pods")

		if err := kube.Tracker().Delete(gvr, testNamespace, name); err != nil {
			return true, nil, err
		}

		pods, err := kube.Tracker().List(gvr, corev1.SchemeGroupVersion.WithKind("Pod"), testNamespace)
		if err != nil {
			return true, nil, err
		}

		used := map[string]bool{}

		for _, pod := range pods.(*corev1.PodList).Items {
			for _, v := range pod.Spec.Volumes {
				if v.PersistentVolumeClaim != nil {
					used[v.PersistentVolumeClaim.ClaimName] = true
				}
			}
		}

		pvcGVR := corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims")

		list, err := kube.Tracker().List(pvcGVR, corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"), testNamespace)
		if err != nil {
			return true, nil, err
		}

		for _, pvc := range list.(*corev1.PersistentVolumeClaimList).Items {
			if used[pvc.Name] || !slices.Contains(pvc.Finalizers, pvcProtectionFinalizer) {
				continue
			}

			pvc.Finalizers = slices.DeleteFunc(pvc.Finalizers, func(f string) bool { return f == pvcProtectionFinalizer })

			if err := kube.Tracker().Update(pvcGVR, &pvc, testNamespace); err != nil {
				return true, nil, err
			}
		}

		return true, nil, nil
	})
}

func TestVacate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claims  []*corev1.PersistentVolumeClaim
		objects []runtime.Object
		force   bool
		// replaced holds claims as held, with UIDs that no longer match the live ones.
		replaced     bool
		timeout      time.Duration
		wantErr      string
		wantNodes    []string
		wantSets     map[string][]string
		wantPodsLeft int
	}{
		{
			name:   "not in use",
			claims: []*corev1.PersistentVolumeClaim{heldPVC("a", false), heldPVC("b", false)},
		},
		{
			name:         "in use without force",
			claims:       []*corev1.PersistentVolumeClaim{heldPVC("a", true)},
			objects:      []runtime.Object{claimPod("web-0", "node-1", "web", "a")},
			wantErr:      "persistentvolumeclaims a started being used by pods: web-0 while it was held, re-run with --force",
			wantNodes:    []string{"node-1"},
			wantSets:     map[string][]string{"web": {"a"}},
			wantPodsLeft: 1,
		},
		{
			name:   "in use with force",
			claims: []*corev1.PersistentVolumeClaim{heldPVC("a", true), heldPVC("b", true)},
			objects: []runtime.Object{
				claimPod("web-0", "node-1", "web", "a"),
				claimPod("db-0", "node-2", "db", "b"),
				claimPod("both", "node-1", "", "a", "b"),
				claimPod("other", "node-3", "other", "c"),
			},
			force:        true,
			wantNodes:    []string{"node-1", "node-2"},
			wantSets:     map[string][]string{"web": {"a"}, "db": {"b"}},
			wantPodsLeft: 1,
		},
		{
			name:     "claim replaced",
			claims:   []*corev1.PersistentVolumeClaim{heldPVC("a", false)},
			replaced: true,
			wantErr:  "persistentvolumeclaims a was replaced while it was held",
		},
		{
			name:         "still protected when the context expires",
			claims:       []*corev1.PersistentVolumeClaim{heldPVC("a", true)},
			objects:      []runtime.Object{claimPod("unrelated", "node-3", "", "c")},
			force:        true,
			timeout:      20 * time.Millisecond,
			wantErr:      context.DeadlineExceeded.Error(),
			wantPodsLeft: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			objects := append([]runtime.Object{}, tt.objects...)
			for _, c := range tt.claims {
				objects = append(objects, c.DeepCopy())
			}

			kube := fake.NewClientset(objects...)
			unprotectOnPodDelete(kube)

			claims := tt.claims
			if tt.replaced {
				claims = []*corev1.PersistentVolumeClaim{}

				for _, c := range tt.claims {
					c = c.DeepCopy()
					c.UID = "old-" + c.UID
					claims = append(claims, c)
				}
			}

			ctx := context.Background()

			if tt.timeout > 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}

			got, err := vacate(ctx, kube, testNamespace, claims, tt.force)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.ElementsMatch(t, tt.wantNodes, got.Nodes)

			if tt.wantSets == nil {
				tt.wantSets = map[string][]string{}
			}

			assert.Equal(t, tt.wantSets, got.StatefulSets)

			pods, err := kube.CoreV1().Pods(testNamespace).List(context.Background(), metav1.ListOptions{})
			require.NoError(t, err)

			assert.Len(t, pods.Items, tt.wantPodsLeft)
		})
	}
}

func TestVacateDeletesRecreatedPod(t *testing.T) {
	t.Parallel()

	kube := fake.NewClientset(heldPVC("a", true), claimPod("web-0", "node-1", "web", "a"))
	unprotectOnPodDelete(kube)

	deletes := []string{}

	// The first delete of web-0 is answered by a new web-0 using the claim, as a controller
	// that does not check the claim would.
	kube.PrependReactor("delete", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		uid := action.(clienttesting.DeleteActionImpl).DeleteOptions.Preconditions.UID
		deletes = append(deletes, string(*uid))

		if len(deletes) == 1 {
			pod := claimPod("web-0", "node-2", "web", "a")
			pod.UID = "uid-web-0-new"
			gvr := corev1.SchemeGroupVersion.WithResource("pods")

			if err := kube.Tracker().Delete(gvr, testNamespace, "web-0"); err != nil {
				return true, nil, err
			}

			return true, nil, kube.Tracker().Add(pod)
		}

		return false, nil, nil
	})

	got, err := vacate(context.Background(), kube, testNamespace, []*corev1.PersistentVolumeClaim{heldPVC("a", true)}, true)
	require.NoError(t, err)

	assert.Equal(t, []string{"uid-web-0", "uid-web-0-new"}, deletes)
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, got.Nodes)
	assert.Equal(t, map[string][]string{"web": {"a"}}, got.StatefulSets)
}

func TestTriggerStatefulSets(t *testing.T) {
	t.Parallel()

	sts := func(name string) *appsv1.StatefulSet {
		return &appsv1.StatefulSet{Name: name, Namespace: testNamespace}
	}

	// patched records the StatefulSets patched, and runs onPatch before each patch.
	patched := func(kube *fake.Clientset, onPatch func(name string, n int)) *[]string {
		names := []string{}

		kube.PrependReactor("patch", "statefulsets", func(action clienttesting.Action) (bool, runtime.Object, error) {
			name := action.(clienttesting.PatchAction).GetName()
			names = append(names, name)

			if onPatch != nil {
				onPatch(name, len(names))
			}

			return false, nil, nil
		})

		return &names
	}

	t.Run("pods already exist", func(t *testing.T) {
		t.Parallel()

		kube := fake.NewClientset(sts("web"), livePVC("a"), livePVC("b"), claimPod("web-0", "", "web", "a"), claimPod("web-1", "", "web", "b"))
		names := patched(kube, nil)

		triggerStatefulSets(context.Background(), kube, testNamespace, map[string][]string{"web": {"a", "b"}})

		assert.Empty(t, *names)
	})

	t.Run("patched until the statefulset has its pod", func(t *testing.T) {
		t.Parallel()

		kube := fake.NewClientset(sts("web"), livePVC("a"))
		names := patched(kube, func(_ string, n int) {
			if n == 2 {
				assert.NoError(t, kube.Tracker().Add(claimPod("web-0", "", "web", "a")))
			}
		})

		triggerStatefulSets(context.Background(), kube, testNamespace, map[string][]string{"web": {"a"}})

		assert.Equal(t, []string{"web", "web"}, *names)

		got, err := kube.AppsV1().StatefulSets(testNamespace).Get(context.Background(), "web", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Contains(t, got.Annotations, annReboundAt)
	})

	t.Run("each statefulset sharing a claim waits for its own pod", func(t *testing.T) {
		t.Parallel()

		kube := fake.NewClientset(sts("web"), sts("db"), livePVC("a"), claimPod("web-0", "", "web", "a"))
		names := patched(kube, func(name string, _ int) {
			if name == "db" {
				assert.NoError(t, kube.Tracker().Add(claimPod("db-0", "", "db", "a")))
			}
		})

		triggerStatefulSets(context.Background(), kube, testNamespace, map[string][]string{"web": {"a"}, "db": {"a"}})

		assert.Equal(t, []string{"db"}, *names)
	})

	t.Run("claims not in place are skipped", func(t *testing.T) {
		t.Parallel()

		kube := fake.NewClientset(sts("web"), sts("db"), heldPVC("a", false))
		names := patched(kube, nil)

		triggerStatefulSets(context.Background(), kube, testNamespace, map[string][]string{"web": {"a"}, "db": {"gone"}})

		assert.Empty(t, *names)
	})

	t.Run("gives up after the attempts", func(t *testing.T) {
		t.Parallel()

		kube := fake.NewClientset(sts("web"), livePVC("a"))
		names := patched(kube, nil)

		triggerStatefulSets(context.Background(), kube, testNamespace, map[string][]string{"web": {"a"}, "gone": {"a"}})

		assert.Len(t, *names, 2*triggerAttempts)
	})

	t.Run("nothing to trigger", func(t *testing.T) {
		t.Parallel()

		kube := fake.NewClientset()

		triggerStatefulSets(context.Background(), kube, testNamespace, nil)

		assert.Empty(t, kube.Actions())
	})
}

func TestCommandTimeout(t *testing.T) {
	t.Parallel()

	got, err := commandTimeout(0, 600)
	require.NoError(t, err)
	assert.Equal(t, 600*time.Second, got)

	got, err = commandTimeout(30, 600)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, got)

	_, err = commandTimeout(9223372037, 600)
	require.Error(t, err)
}
