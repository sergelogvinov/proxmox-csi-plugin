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
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const namespace = "default"

var (
	pvcResource = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	pvResource  = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
)

// fakeKube is a fake clientset that adds the apiserver behavior the rebind module relies on,
// which the plain fake lacks: UIDs and resource versions, UID and resource version
// preconditions, finalizers keeping a deleted object Terminating, and deleting it once the
// last finalizer is removed. It also binds a created claim to the volume it names, as the PV
// controller would, and can fail one chosen write.
type fakeKube struct {
	*fake.Clientset

	// failAt makes the failAt-th write (create, update, patch or delete) fail. Zero never
	// fails. With lost set, the write is applied but its response is lost.
	failAt int
	lost   bool
	writes int

	version int

	// violations collects volumes that were deleted while not retained, and volumes that were
	// reserved for a claim by name only, which any claim with that name could bind.
	violations []string
}

func newFakeKube(t *testing.T, objs ...runtime.Object) *fakeKube {
	t.Helper()

	f := &fakeKube{}

	for _, obj := range objs {
		if meta, ok := obj.(metav1.Object); ok && meta.GetResourceVersion() == "" {
			meta.SetResourceVersion(f.nextVersion())
		}
	}

	f.Clientset = fake.NewClientset(objs...)
	f.PrependReactor("*", "*", f.react)

	return f
}

func (f *fakeKube) react(action k8stesting.Action) (bool, runtime.Object, error) {
	res := action.GetResource()
	if res != pvcResource && res != pvResource {
		return false, nil, nil
	}

	switch action.GetVerb() {
	case "create", "update", "patch", "delete":
	default:
		return false, nil, nil
	}

	f.writes++

	if f.writes == f.failAt && !f.lost {
		return true, nil, fmt.Errorf("injected failure at write %d (%s %s)", f.writes, action.GetVerb(), res.Resource)
	}

	obj, err := f.write(action)

	if f.writes == f.failAt && f.lost {
		return true, nil, fmt.Errorf("injected lost response at write %d (%s %s, applied: %v)", f.writes, action.GetVerb(), res.Resource, err == nil)
	}

	return true, obj, err
}

func (f *fakeKube) write(action k8stesting.Action) (runtime.Object, error) {
	res := action.GetResource()
	tracker := f.Tracker()

	switch action.GetVerb() {
	case "create":
		a := action.(k8stesting.CreateAction) //nolint: errcheck
		obj := a.GetObject().DeepCopyObject()
		meta, _ := obj.(metav1.Object) //nolint: errcheck

		meta.SetUID(uuid.NewUUID())
		meta.SetCreationTimestamp(metav1.Now())
		meta.SetResourceVersion(f.nextVersion())

		if err := tracker.Create(res, obj, a.GetNamespace()); err != nil {
			return nil, err
		}

		f.checkReservation(obj)
		f.bind()

		return tracker.Get(res, a.GetNamespace(), meta.GetName())

	case "delete":
		a := action.(k8stesting.DeleteAction) //nolint: errcheck

		obj, err := tracker.Get(res, a.GetNamespace(), a.GetName())
		if err != nil {
			return nil, err
		}

		meta, _ := obj.(metav1.Object) //nolint: errcheck

		if pre := a.GetDeleteOptions().Preconditions; pre != nil {
			if pre.UID != nil && *pre.UID != meta.GetUID() {
				return nil, apierrors.NewConflict(res.GroupResource(), a.GetName(), fmt.Errorf("uid precondition failed"))
			}

			if pre.ResourceVersion != nil && *pre.ResourceVersion != meta.GetResourceVersion() {
				return nil, apierrors.NewConflict(res.GroupResource(), a.GetName(), fmt.Errorf("resource version precondition failed"))
			}
		}

		if pv, ok := obj.(*corev1.PersistentVolume); ok && pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
			f.violations = append(f.violations, fmt.Sprintf("volume %s deleted with reclaim policy %s", pv.Name, pv.Spec.PersistentVolumeReclaimPolicy))
		}

		if len(meta.GetFinalizers()) == 0 {
			return nil, tracker.Delete(res, a.GetNamespace(), a.GetName())
		}

		if meta.GetDeletionTimestamp() == nil {
			now := metav1.Now()
			meta.SetDeletionTimestamp(&now)
			meta.SetResourceVersion(f.nextVersion())

			return nil, tracker.Update(res, obj, a.GetNamespace())
		}

		return nil, nil

	case "update":
		a := action.(k8stesting.UpdateAction) //nolint: errcheck
		obj := a.GetObject().DeepCopyObject()
		meta, _ := obj.(metav1.Object) //nolint: errcheck

		current, err := tracker.Get(res, a.GetNamespace(), meta.GetName())
		if err != nil {
			return nil, err
		}

		if rv := meta.GetResourceVersion(); rv != "" && rv != current.(metav1.Object).GetResourceVersion() { //nolint: errcheck
			return nil, apierrors.NewConflict(res.GroupResource(), meta.GetName(), fmt.Errorf("object has been modified"))
		}

		if meta.GetDeletionTimestamp() != nil && len(meta.GetFinalizers()) == 0 {
			return obj, tracker.Delete(res, a.GetNamespace(), meta.GetName())
		}

		meta.SetResourceVersion(f.nextVersion())

		return obj, tracker.Update(res, obj, a.GetNamespace())

	case "patch":
		a := action.(k8stesting.PatchAction) //nolint: errcheck

		if _, _, err := k8stesting.ObjectReaction(tracker)(action); err != nil {
			return nil, err
		}

		obj, err := tracker.Get(res, a.GetNamespace(), a.GetName())
		if err != nil {
			return nil, err
		}

		obj.(metav1.Object).SetResourceVersion(f.nextVersion()) //nolint: errcheck

		if err = tracker.Update(res, obj, a.GetNamespace()); err != nil {
			return nil, err
		}

		f.checkReservation(obj)
		f.bind()

		return tracker.Get(res, a.GetNamespace(), a.GetName())
	}

	return nil, fmt.Errorf("unexpected verb %s", action.GetVerb())
}

// checkReservation records a volume written with a claimRef that has no UID.
func (f *fakeKube) checkReservation(obj runtime.Object) {
	if pv, ok := obj.(*corev1.PersistentVolume); ok && pv.Spec.ClaimRef != nil && pv.Spec.ClaimRef.UID == "" {
		f.violations = append(f.violations, fmt.Sprintf("volume %s reserved for claim %s by name only", pv.Name, pv.Spec.ClaimRef.Name))
	}
}

func (f *fakeKube) nextVersion() string {
	f.version++

	return fmt.Sprintf("%d", f.version)
}

// bind binds every unbound claim to the volume it names, once that volume is reserved for it.
func (f *fakeKube) bind() {
	tracker := f.Tracker()

	list, err := tracker.List(pvcResource, schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolumeClaim"}, namespace)
	if err != nil {
		return
	}

	for _, pvc := range list.(*corev1.PersistentVolumeClaimList).Items {
		if pvc.Status.Phase == corev1.ClaimBound || pvc.Spec.VolumeName == "" || pvc.DeletionTimestamp != nil {
			continue
		}

		obj, err := tracker.Get(pvResource, "", pvc.Spec.VolumeName)
		if err != nil {
			continue
		}

		pv := obj.(*corev1.PersistentVolume) //nolint: errcheck

		ref := pv.Spec.ClaimRef
		if ref == nil || ref.Namespace != pvc.Namespace || ref.Name != pvc.Name || (ref.UID != "" && ref.UID != pvc.UID) {
			continue
		}

		pv.Spec.ClaimRef.UID = pvc.UID
		pv.Status.Phase = corev1.VolumeBound
		pv.ResourceVersion = f.nextVersion()

		pvc.Status.Phase = corev1.ClaimBound
		pvc.Status.Capacity = pv.Spec.Capacity
		pvc.ResourceVersion = f.nextVersion()

		_ = tracker.Update(pvResource, pv, "")           //nolint: errcheck
		_ = tracker.Update(pvcResource, &pvc, namespace) //nolint: errcheck
	}
}

// boundPair returns a claim bound to a CSI volume on node1, with the volume using policy.
func boundPair(claim, volume string, policy corev1.PersistentVolumeReclaimPolicy) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolume) {
	size := resource.MustParse("10Gi")

	pvc := &corev1.PersistentVolumeClaim{
		Name:      claim,
		Namespace: namespace,
		UID:       uuid.NewUUID(),
		Annotations: map[string]string{
			"pv.kubernetes.io/bind-completed":      "yes",
			"pv.kubernetes.io/bound-by-controller": "yes",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: volume,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:    corev1.ClaimBound,
			Capacity: corev1.ResourceList{corev1.ResourceStorage: size},
		},
	}

	pv := &corev1.PersistentVolume{
		Name: volume, UID: uuid.NewUUID(),
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: size},
			PersistentVolumeReclaimPolicy: policy,
			ClaimRef: &corev1.ObjectReference{
				Kind: "PersistentVolumeClaim", APIVersion: "v1",
				Namespace: namespace, Name: claim, UID: pvc.UID,
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "csi.proxmox.sinextra.dev",
					VolumeHandle: "region-1/node1/local-lvm/vm-9999-" + volume,
				},
			},
			NodeAffinity: affinity("node1"),
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}

	return pvc, pv
}

func affinity(zone string) *corev1.VolumeNodeAffinity {
	return &corev1.VolumeNodeAffinity{
		Required: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: corev1.LabelTopologyRegion, Operator: corev1.NodeSelectorOpIn, Values: []string{"region-1"}},
					{Key: corev1.LabelTopologyZone, Operator: corev1.NodeSelectorOpIn, Values: []string{zone}},
				},
			}},
		},
	}
}
