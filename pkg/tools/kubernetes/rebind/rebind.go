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

// Package rebind recreates PersistentVolumeClaims against their existing PersistentVolumes
// (under a new name, with each other's volume, or with a new volume topology) without ever
// letting the underlying disk be released.
//
// A Rebind has two phases. Hold pins each volume to Retain, records its original reclaim
// policy, and deletes the claim while a finalizer keeps it Terminating, so no pod can start
// with it and no StatefulSet can recreate it. Commit then recreates every held claim as its
// Target; Abort puts the claims back as they were. Drain, between the two, waits until no pod
// can use the held claims anymore, deleting their pods in force mode; Commit refuses to run
// before. Work that has to happen while the claims are held and unused, such as moving a disk,
// runs after Drain. TriggerStatefulSets, after Commit or Abort, gets the StatefulSets of the
// drained pods to create them again right away.
package rebind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const (
	// Finalizer keeps a held claim Terminating until the Rebind is committed or aborted.
	Finalizer = csi.DriverName + "/rebind"

	// legacyFinalizer was used by pvecsictl migrate before this package existed. A claim
	// still carrying it is adopted by Hold and released like one carrying Finalizer.
	legacyFinalizer = csi.DriverName + "/migrate"

	// AnnReclaimPolicy records the reclaim policy a volume had before Hold switched it to
	// Retain, so a re-run after an interruption, or an operator, can still restore it.
	AnnReclaimPolicy = csi.DriverName + "/reclaim-policy"

	// annHeldVolume records on a held claim the UID of its volume, so a resumed Hold can tell
	// that the volume was not replaced in between.
	annHeldVolume = csi.DriverName + "/held-volume"

	// AnnRebindFrom marks a claim or volume created by a Rebind with the UID of the held object
	// it replaces, so a re-run can tell its own objects from ones created by someone else.
	AnnRebindFrom = csi.DriverName + "/rebind-from"

	// pvcProtectionFinalizer is held by Kubernetes while a scheduled pod uses a claim.
	pvcProtectionFinalizer = "kubernetes.io/pvc-protection"

	// Binding annotations the PV controller sets on a bound claim. A recreated claim must
	// not carry them: with bind-completed, the controller treats it as already bound and
	// marks it Lost.
	annBindCompleted     = "pv.kubernetes.io/bind-completed"
	annBoundByController = "pv.kubernetes.io/bound-by-controller"
)

var (
	// ErrNotBound is returned by Hold for a claim that is neither bound to its volume nor
	// already held.
	ErrNotBound = errors.New("claim is not bound")

	// ErrInUse is returned while a pod still holds a claim (kubernetes.io/pvc-protection).
	ErrInUse = errors.New("claim is in use")

	// ErrNameTaken is returned by Commit when a target name is invalid or belongs to a
	// claim that is not part of the Rebind.
	ErrNameTaken = errors.New("claim name is taken")

	// ErrCommitted is returned by Abort once Commit has started changing objects.
	ErrCommitted = errors.New("rebind is already being committed")
)

// Target is what a held claim becomes on Commit.
type Target struct {
	// Claim is the name of the held claim this target applies to.
	Claim string
	// NewName is the name of the recreated claim. Empty keeps the name.
	NewName string
	// VolumeHandle is the new CSI volume handle of the volume. Empty keeps it.
	VolumeHandle string
	// NodeAffinity is the new node affinity of the volume. Nil keeps it.
	NodeAffinity *corev1.VolumeNodeAffinity
}

// Rebind is a set of held claims on their way to their targets. It is not safe for concurrent use.
type Rebind struct {
	// Logf, if set, receives progress messages of Drain and TriggerStatefulSets.
	Logf func(format string, args ...any)

	kube      kubernetes.Interface
	namespace string
	claims    []*heldClaim
	committed bool
}

type heldClaim struct {
	pvc    *corev1.PersistentVolumeClaim
	pv     *corev1.PersistentVolume
	policy corev1.PersistentVolumeReclaimPolicy

	// recreating is set once a release started recreating the claim, released once the
	// release is done.
	recreating bool
	released   bool

	// statefulSets are the StatefulSets of the pods Drain saw referencing the claim.
	statefulSets []string
}

// plan is a held claim paired with its target.
type plan struct {
	*heldClaim

	name     string
	recreate bool
	newPV    *corev1.PersistentVolume
}

// Hold holds the named claims. Each one must be bound to its volume, or already held by an
// earlier Hold that was interrupted. Every claim is checked before any is changed, and if
// holding one of them fails, the ones already held are released again.
//
// Hold does not wait for pods using the claims: the claims stay Terminating with
// kubernetes.io/pvc-protection until those pods are gone, and Commit refuses to run before.
// Drain waits for them.
func Hold(ctx context.Context, kube kubernetes.Interface, namespace string, claims ...string) (*Rebind, error) {
	if len(claims) == 0 {
		return nil, errors.New("rebind: no claims to hold")
	}

	h := &Rebind{kube: kube, namespace: namespace}

	for i, name := range claims {
		if slices.Contains(claims[:i], name) {
			return nil, fmt.Errorf("rebind: claim %s is listed twice", name)
		}

		c, err := h.read(ctx, name)
		if err != nil {
			return nil, err
		}

		h.claims = append(h.claims, c)
	}

	for i, c := range h.claims {
		if err := h.hold(ctx, c); err != nil {
			// The caller's context may be what failed: roll back on a context of its own.
			undoCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			undoErr := h.release(undoCtx, h.claims[:i+1])

			cancel()

			if undoErr != nil {
				return nil, fmt.Errorf("%w; releasing the claims failed, they are left held: %w", err, undoErr)
			}

			return nil, err
		}
	}

	return h, nil
}

// Commit recreates every held claim as its target. Held claims without a target are
// recreated as they were. It refuses to start while a pod still holds a claim, or when a
// target name is taken by a claim outside the Rebind.
//
// A Commit that failed can be called again with the same targets, and continues where it
// stopped.
func (h *Rebind) Commit(ctx context.Context, targets ...Target) error {
	if slices.ContainsFunc(h.claims, func(c *heldClaim) bool { return c.recreating || c.released }) {
		return errors.New("rebind: already aborted")
	}

	plans, err := h.plan(targets)
	if err != nil {
		return err
	}

	if err = h.check(ctx, plans); err != nil {
		return err
	}

	h.committed = true

	return h.apply(ctx, plans)
}

// Abort puts the held claims back as they were. It is only possible until Commit has
// started. A claim a pod still holds is left held, while the others are put back, and the
// error wraps ErrInUse. An Abort that failed can be called again, and continues where it
// stopped.
func (h *Rebind) Abort(ctx context.Context) error {
	if h.committed {
		return ErrCommitted
	}

	return h.release(ctx, h.claims)
}

// Manifests returns the objects Commit creates for the targets: a claim for each held claim,
// followed by a volume for each one whose topology changes. They describe how to finish the
// Rebind by hand: a volume is retained and reserved for its claim by name only, so the UID
// of the created claim has to be set in its claimRef before it is created.
func (h *Rebind) Manifests(targets ...Target) ([]runtime.Object, error) {
	plans, err := h.plan(targets)
	if err != nil {
		return nil, err
	}

	objs := []runtime.Object{}

	for _, p := range plans {
		pvc := newClaim(p.pvc, p.name)
		pvc.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: kindClaim}
		objs = append(objs, pvc)

		if p.recreate {
			pv := p.newPV.DeepCopy()
			pv.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolume"}
			objs = append(objs, pv)
		}
	}

	return objs, nil
}

// read fetches a claim and its volume and checks that the claim can be held.
func (h *Rebind) read(ctx context.Context, name string) (*heldClaim, error) {
	pvc, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("rebind: get claim %s: %w", name, err)
	}

	if pvc.Spec.VolumeName == "" {
		return nil, fmt.Errorf("rebind: claim %s has no volume: %w", name, ErrNotBound)
	}

	pv, err := h.kube.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("rebind: get volume %s of claim %s: %w", pvc.Spec.VolumeName, name, err)
	}

	if IsHeld(pvc) {
		// The volume must still be the one the hold was taken on. A hold from before the
		// volume UID was recorded must still be reserved for the claim.
		recorded := pvc.Annotations[annHeldVolume]
		if (recorded != "" && recorded != string(pv.UID)) ||
			(recorded == "" && (pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != pvc.UID)) {
			return nil, fmt.Errorf("rebind: volume %s of held claim %s was replaced: %w", pv.Name, name, ErrNotBound)
		}
	} else {
		if pvc.Status.Phase != corev1.ClaimBound || pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != pvc.UID {
			return nil, fmt.Errorf("rebind: claim %s and volume %s are not bound to each other: %w", name, pv.Name, ErrNotBound)
		}

		if pvc.DeletionTimestamp != nil {
			return nil, fmt.Errorf("rebind: claim %s is being deleted: %w", name, ErrNotBound)
		}
	}

	// A recreated claim requests the capacity it has, as its volume cannot hold more.
	// Refuse instead of dropping an expansion that is still in progress, held or not.
	request, requested := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	capacity, known := pvc.Status.Capacity[corev1.ResourceStorage]

	if requested && known && request.Cmp(capacity) > 0 {
		return nil, fmt.Errorf("rebind: claim %s is being expanded from %s to %s, finish or revert the expansion first",
			name, capacity.String(), request.String())
	}

	return &heldClaim{pvc: pvc, pv: pv, policy: originalPolicy(pv)}, nil
}

// hold retains the volume and deletes the claim behind Finalizer. All of it is idempotent.
func (h *Rebind) hold(ctx context.Context, c *heldClaim) error {
	err := h.retainVolume(ctx, c)
	if err != nil {
		return err
	}

	if !IsHeld(c.pvc) {
		if err = h.addFinalizer(ctx, c); err != nil {
			return err
		}
	}

	if c.pvc.DeletionTimestamp == nil {
		opts := metav1.DeleteOptions{Preconditions: metav1.NewUIDPreconditions(string(c.pvc.UID))}
		if err = h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Delete(ctx, c.pvc.Name, opts); err != nil {
			return fmt.Errorf("rebind: delete claim %s: %w", c.pvc.Name, err)
		}
	}

	pvc, err := h.liveClaim(ctx, c)
	if err != nil {
		return err
	}

	if pvc == nil {
		return fmt.Errorf("rebind: claim %s was removed while being held", c.pvc.Name)
	}

	c.pvc = pvc

	return nil
}

// retainVolume switches the held volume to Retain and records its original policy.
func (h *Rebind) retainVolume(ctx context.Context, c *heldClaim) error {
	for attempt := 0; ; attempt++ {
		// Patch only the version the original policy was read from, so neither that policy nor
		// annotations added meanwhile are lost.
		ops := []jsonOp{{Op: "test", Path: "/metadata/resourceVersion", Value: c.pv.ResourceVersion}}

		// An empty map is left out of the object's JSON, so its path does not exist either.
		if len(c.pv.Annotations) == 0 {
			ops = append(ops, jsonOp{Op: opAdd, Path: "/metadata/annotations", Value: map[string]string{}})
		}

		ops = append(ops,
			jsonOp{Op: opAdd, Path: annotationPath(AnnReclaimPolicy), Value: string(c.policy)},
			jsonOp{Op: opAdd, Path: pathReclaimPolicy, Value: corev1.PersistentVolumeReclaimRetain},
		)

		pv, err := h.patchVolume(ctx, c.pv.Name, c.pv.UID, ops...)
		if err == nil {
			c.pv = pv

			return nil
		}

		live, getErr := h.kube.CoreV1().PersistentVolumes().Get(ctx, c.pv.Name, metav1.GetOptions{})
		if getErr != nil || live.UID != c.pv.UID || live.ResourceVersion == c.pv.ResourceVersion || attempt >= 4 {
			return fmt.Errorf("rebind: retain volume %s: %w", c.pv.Name, err)
		}

		c.pv = live
		c.policy = originalPolicy(live)
	}
}

// release ends the hold on the given claims without a target. A claim that was never
// deleted is released in place, a Terminating one is recreated as it was. A claim that is in
// use or was replaced is left held, the others are still released, and all errors are
// returned together.
func (h *Rebind) release(ctx context.Context, claims []*heldClaim) error {
	var (
		errs  []error
		plans []*plan
	)

	for _, c := range claims {
		if c.released {
			continue
		}

		if c.recreating {
			plans = append(plans, &plan{heldClaim: c, name: c.pvc.Name})

			continue
		}

		pvc, err := h.liveClaim(ctx, c)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		// Keep the volume retained: the original claim may already be gone, and releasing
		// the volume could delete the disk.
		if pvc == nil {
			errs = append(errs, fmt.Errorf("rebind: claim %s was replaced, volume %s is left retained", c.pvc.Name, c.pv.Name))

			continue
		}

		c.pvc = pvc

		if pvc.DeletionTimestamp == nil {
			terminating, err := h.releaseInPlace(ctx, c)
			if err == nil && !terminating {
				err = h.restorePolicy(ctx, c.pv.Name, c.pv.UID, c.policy)
			}

			if err != nil {
				errs = append(errs, err)

				continue
			}

			if !terminating {
				c.released = true

				continue
			}

			// A delete took effect after all: recreate the claim like any Terminating one.
			if pvc, err = h.liveClaim(ctx, c); err != nil {
				errs = append(errs, err)

				continue
			}

			if pvc == nil {
				errs = append(errs, fmt.Errorf("rebind: claim %s was deleted while being released, volume %s is left retained", c.pvc.Name, c.pv.Name))

				continue
			}

			c.pvc = pvc
		}

		if slices.Contains(pvc.Finalizers, pvcProtectionFinalizer) {
			errs = append(errs, fmt.Errorf("rebind: release claim %s: %w", c.pvc.Name, ErrInUse))

			continue
		}

		c.recreating = true
		plans = append(plans, &plan{heldClaim: c, name: c.pvc.Name})
	}

	// Released claims keep their names, so each one is recreated on its own, and one that
	// cannot be does not block the others.
	for _, p := range plans {
		if err := h.apply(ctx, []*plan{p}); err != nil {
			errs = append(errs, err)
		} else {
			p.released = true
		}
	}

	return errors.Join(errs...)
}

// plan pairs every held claim with its target and validates the targets.
func (h *Rebind) plan(targets []Target) ([]*plan, error) {
	byClaim := map[string]Target{}

	for _, t := range targets {
		if !slices.ContainsFunc(h.claims, func(c *heldClaim) bool { return c.pvc.Name == t.Claim }) {
			return nil, fmt.Errorf("rebind: target for claim %s, which is not held", t.Claim)
		}

		if _, ok := byClaim[t.Claim]; ok {
			return nil, fmt.Errorf("rebind: claim %s has more than one target", t.Claim)
		}

		byClaim[t.Claim] = t
	}

	plans := make([]*plan, 0, len(h.claims))
	names := map[string]string{}

	for _, c := range h.claims {
		t := byClaim[c.pvc.Name]

		p := &plan{heldClaim: c, name: c.pvc.Name}

		if t.NewName != "" {
			if errs := validation.IsDNS1123Subdomain(t.NewName); len(errs) > 0 {
				return nil, fmt.Errorf("rebind: invalid claim name %q: %s: %w", t.NewName, strings.Join(errs, ", "), ErrNameTaken)
			}

			p.name = t.NewName
		}

		if other, ok := names[p.name]; ok {
			return nil, fmt.Errorf("rebind: claims %s and %s would both be named %s: %w", other, c.pvc.Name, p.name, ErrNameTaken)
		}

		names[p.name] = c.pvc.Name

		if c.pv.Spec.CSI == nil && t.VolumeHandle != "" {
			return nil, fmt.Errorf("rebind: volume %s is not a CSI volume, its handle cannot be changed", c.pv.Name)
		}

		handleChanged := t.VolumeHandle != "" && t.VolumeHandle != c.pv.Spec.CSI.VolumeHandle
		affinityChanged := t.NodeAffinity != nil && !equality.Semantic.DeepEqual(t.NodeAffinity, c.pv.Spec.NodeAffinity)

		if handleChanged || affinityChanged {
			p.recreate = true
			p.newPV = newVolume(c.pv, c.policy, h.namespace, p.name, t)
		}

		plans = append(plans, p)
	}

	return plans, nil
}

// check verifies, before anything is changed, that no pod holds a claim and that every
// target name is free, part of the Rebind, or already recreated by an earlier Commit.
func (h *Rebind) check(ctx context.Context, plans []*plan) error {
	held := map[types.UID]bool{}
	for _, c := range h.claims {
		held[c.pvc.UID] = true
	}

	for _, p := range plans {
		pvc, err := h.liveClaim(ctx, p.heldClaim)
		if err != nil {
			return err
		}

		if pvc != nil && slices.Contains(pvc.Finalizers, pvcProtectionFinalizer) {
			return fmt.Errorf("rebind: commit claim %s: %w", p.pvc.Name, ErrInUse)
		}
	}

	for _, p := range plans {
		live, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, p.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}

		if err != nil {
			return fmt.Errorf("rebind: get claim %s: %w", p.name, err)
		}

		if held[live.UID] || live.Annotations[AnnRebindFrom] == string(p.pvc.UID) {
			continue
		}

		return fmt.Errorf("rebind: claim %s already exists: %w", p.name, ErrNameTaken)
	}

	return nil
}

// apply recreates the planned claims. Every step tolerates having been done by an earlier,
// interrupted run.
func (h *Rebind) apply(ctx context.Context, plans []*plan) error {
	// Delete volumes that are recreated with a new topology first: a bound volume stays
	// Terminating, and deleting it early keeps the gap between the old claim going away and
	// the new one appearing short.
	for _, p := range plans {
		if p.recreate {
			if err := h.deleteVolume(ctx, p); err != nil {
				return err
			}
		}
	}

	for _, p := range plans {
		if err := h.deleteClaim(ctx, p); err != nil {
			return err
		}
	}

	// Claims can take each other's names (a Swap), so all of them have to be gone before
	// any is created.
	for _, p := range plans {
		if err := h.waitClaimDeleted(ctx, p.pvc.Name, p.pvc.UID); err != nil {
			return fmt.Errorf("rebind: wait for claim %s to be deleted: %w", p.pvc.Name, err)
		}
	}

	// Create the claims before reserving their volumes, so each volume can be reserved for
	// the UID of its claim, and no other claim with that name can bind it. A claim also has
	// to be created before a recreated volume: once the old claim is gone, a StatefulSet
	// would recreate it from its template. The claim names its volume, so it stays Pending
	// until the volume is reserved for it.
	uids := make([]types.UID, len(plans))

	for i, p := range plans {
		uid, err := h.createClaim(ctx, p)
		if err != nil {
			return err
		}

		uids[i] = uid
	}

	// Every volume stays retained until every claim has its volume reserved: a claim of the
	// batch that goes away before then must never take its disk with it.
	volumes := make([]types.UID, len(plans))

	for i, p := range plans {
		if p.recreate {
			uid, err := h.createVolume(ctx, p, uids[i])
			if err != nil {
				return err
			}

			volumes[i] = uid

			continue
		}

		if err := h.verifyClaim(ctx, p.name, uids[i]); err != nil {
			return err
		}

		if err := h.reserveVolume(ctx, p.pv.Name, p.pv.UID, p.name, uids[i]); err != nil {
			return err
		}

		volumes[i] = p.pv.UID
	}

	for i, p := range plans {
		if err := h.verifyClaim(ctx, p.name, uids[i]); err != nil {
			return err
		}

		if err := h.restorePolicy(ctx, p.pv.Name, volumes[i], p.policy); err != nil {
			return err
		}
	}

	return nil
}

// verifyClaim checks that the recreated claim still exists, so its volume is never reserved,
// or released to its original policy, for a claim that is gone.
func (h *Rebind) verifyClaim(ctx context.Context, name string, uid types.UID) error {
	live, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("rebind: get claim %s: %w", name, err)
	}

	if err != nil || live.UID != uid || live.DeletionTimestamp != nil {
		return fmt.Errorf("rebind: recreated claim %s was deleted or replaced, its volume is left retained, run Commit again", name)
	}

	return nil
}

// deleteVolume deletes the held volume, enforcing Retain on the live object first.
func (h *Rebind) deleteVolume(ctx context.Context, p *plan) error {
	pv, err := h.kube.CoreV1().PersistentVolumes().Get(ctx, p.pv.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("rebind: get volume %s: %w", p.pv.Name, err)
	}

	// Already recreated by an earlier run.
	if pv.UID != p.pv.UID {
		return nil
	}

	if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		pv, err = h.patchVolume(ctx, pv.Name, pv.UID,
			jsonOp{Op: opAdd, Path: pathReclaimPolicy, Value: corev1.PersistentVolumeReclaimRetain})
		if err != nil {
			return fmt.Errorf("rebind: retain volume %s: %w", p.pv.Name, err)
		}
	}

	if pv.DeletionTimestamp != nil {
		return nil
	}

	// Delete only the version seen as Retain: deleting a volume whose policy was switched
	// back to Delete in the meantime would delete the disk.
	pre := metav1.Preconditions{UID: &pv.UID}
	if pv.ResourceVersion != "" {
		pre.ResourceVersion = &pv.ResourceVersion
	}

	err = h.kube.CoreV1().PersistentVolumes().Delete(ctx, pv.Name, metav1.DeleteOptions{Preconditions: &pre})
	if apierrors.IsConflict(err) {
		return fmt.Errorf("rebind: volume %s changed while being deleted, run Commit again: %w", pv.Name, err)
	}

	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("rebind: delete volume %s: %w", pv.Name, err)
	}

	return nil
}

// deleteClaim deletes the held claim, if it still exists, and releases it from Finalizer.
func (h *Rebind) deleteClaim(ctx context.Context, p *plan) error {
	pvc, err := h.liveClaim(ctx, p.heldClaim)
	if err != nil || pvc == nil {
		return err
	}

	if pvc.DeletionTimestamp == nil {
		opts := metav1.DeleteOptions{Preconditions: metav1.NewUIDPreconditions(string(pvc.UID))}
		if err = h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Delete(ctx, pvc.Name, opts); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("rebind: delete claim %s: %w", pvc.Name, err)
		}
	}

	return h.removeFinalizers(ctx, p.heldClaim)
}

// reserveVolume points the volume's claimRef at the claim with the given UID, unless it
// already does. The claimRef is replaced as a whole, so nothing of an earlier claim survives.
func (h *Rebind) reserveVolume(ctx context.Context, name string, uid types.UID, claim string, claimUID types.UID) error {
	pv, err := h.kube.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("rebind: get volume %s: %w", name, err)
	}

	if pv.UID != uid {
		return fmt.Errorf("rebind: volume %s was replaced, uid %s instead of %s", name, pv.UID, uid)
	}

	if pv.DeletionTimestamp != nil {
		return fmt.Errorf("rebind: volume %s is being deleted", name)
	}

	if ref := pv.Spec.ClaimRef; ref != nil && ref.Namespace == h.namespace && ref.Name == claim && ref.UID == claimUID {
		return nil
	}

	_, err = h.patchVolume(ctx, name, uid, jsonOp{Op: opAdd, Path: "/spec/claimRef", Value: claimRef(h.namespace, claim, claimUID)})
	if err != nil {
		return fmt.Errorf("rebind: reserve volume %s for claim %s: %w", name, claim, err)
	}

	return nil
}

// createClaim creates the recreated claim and returns its UID. A claim with that name that
// is marked with the held claim's UID was created by an earlier run, even if its response
// was lost.
func (h *Rebind) createClaim(ctx context.Context, p *plan) (types.UID, error) {
	created, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Create(ctx, newClaim(p.pvc, p.name), metav1.CreateOptions{})
	if err == nil {
		return created.UID, nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("rebind: create claim %s: %w", p.name, err)
	}

	live, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, p.name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("rebind: get claim %s: %w", p.name, err)
	}

	if live.Annotations[AnnRebindFrom] != string(p.pvc.UID) {
		return "", fmt.Errorf("rebind: claim %s was created by someone else before the rebind could create it: %w", p.name, ErrNameTaken)
	}

	return live.UID, nil
}

// createVolume creates the volume with its new topology once the old one is gone, reserved
// for the claim with the given UID.
func (h *Rebind) createVolume(ctx context.Context, p *plan, claimUID types.UID) (types.UID, error) {
	if err := h.waitVolumeDeleted(ctx, p.pv.Name, p.pv.UID); err != nil {
		return "", fmt.Errorf("rebind: wait for volume %s to be deleted: %w", p.pv.Name, err)
	}

	// The claim may have gone away while the old volume was being deleted.
	if err := h.verifyClaim(ctx, p.name, claimUID); err != nil {
		return "", err
	}

	pv := p.newPV.DeepCopy()
	pv.Spec.ClaimRef.UID = claimUID

	created, err := h.kube.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err == nil {
		return created.UID, nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("rebind: create volume %s: %w", p.pv.Name, err)
	}

	live, err := h.kube.CoreV1().PersistentVolumes().Get(ctx, p.pv.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("rebind: get volume %s: %w", p.pv.Name, err)
	}

	if live.UID == p.pv.UID || live.Annotations[AnnRebindFrom] != string(p.pv.UID) {
		return "", fmt.Errorf("rebind: volume %s was created by someone else before the rebind could create it", p.pv.Name)
	}

	if live.DeletionTimestamp != nil {
		return "", fmt.Errorf("rebind: volume %s created by an earlier run is being deleted, its disk is retained", p.pv.Name)
	}

	// Created by an earlier run, possibly for a claim that has been replaced since.
	return live.UID, h.reserveVolume(ctx, live.Name, live.UID, p.name, claimUID)
}

// restorePolicy puts back the reclaim policy recorded by Hold on the volume with the UID.
func (h *Rebind) restorePolicy(ctx context.Context, name string, uid types.UID, policy corev1.PersistentVolumeReclaimPolicy) error {
	pv, err := h.kube.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("rebind: get volume %s: %w", name, err)
	}

	if pv.UID != uid {
		return fmt.Errorf("rebind: volume %s was replaced, uid %s instead of %s", name, pv.UID, uid)
	}

	ops := []jsonOp{{Op: opAdd, Path: pathReclaimPolicy, Value: policy}}
	if _, ok := pv.Annotations[AnnReclaimPolicy]; ok {
		ops = append(ops, jsonOp{Op: "remove", Path: annotationPath(AnnReclaimPolicy)})
	}

	if _, err = h.patchVolume(ctx, name, uid, ops...); err != nil {
		return fmt.Errorf("rebind: restore reclaim policy of volume %s: %w", name, err)
	}

	return nil
}

const (
	opAdd             = "add"
	pathReclaimPolicy = "/spec/persistentVolumeReclaimPolicy"
	kindClaim         = "PersistentVolumeClaim"
)

// claimRef returns a reference to the claim, reserving a volume for it.
func claimRef(namespace, name string, uid types.UID) *corev1.ObjectReference {
	return &corev1.ObjectReference{Kind: kindClaim, APIVersion: "v1", Namespace: namespace, Name: name, UID: uid}
}

// jsonOp is a JSON Patch operation.
type jsonOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// patchVolume applies the operations to the volume only while it is still the object with
// the given UID, so a volume that was replaced under the same name is never changed.
func (h *Rebind) patchVolume(ctx context.Context, name string, uid types.UID, ops ...jsonOp) (*corev1.PersistentVolume, error) {
	patch, err := json.Marshal(append([]jsonOp{{Op: "test", Path: "/metadata/uid", Value: uid}}, ops...))
	if err != nil {
		return nil, err
	}

	return h.kube.CoreV1().PersistentVolumes().Patch(ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
}

// annotationPath returns the JSON Pointer of an annotation.
func annotationPath(key string) string {
	return "/metadata/annotations/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
}

// liveClaim returns the held claim as it is now, or nil if it is gone or was replaced by
// another object with the same name.
func (h *Rebind) liveClaim(ctx context.Context, c *heldClaim) (*corev1.PersistentVolumeClaim, error) {
	pvc, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, c.pvc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("rebind: get claim %s: %w", c.pvc.Name, err)
	}

	if pvc.UID != c.pvc.UID {
		return nil, nil
	}

	return pvc, nil
}

// IsHeld reports whether the claim is held by a Rebind, including one held by an older
// pvecsictl through the legacy finalizer.
func IsHeld(pvc *corev1.PersistentVolumeClaim) bool {
	return slices.Contains(pvc.Finalizers, Finalizer) || slices.Contains(pvc.Finalizers, legacyFinalizer)
}

// originalPolicy returns the reclaim policy the volume had before Hold switched it to Retain.
func originalPolicy(pv *corev1.PersistentVolume) corev1.PersistentVolumeReclaimPolicy {
	if policy := pv.Annotations[AnnReclaimPolicy]; policy != "" {
		return corev1.PersistentVolumeReclaimPolicy(policy)
	}

	return pv.Spec.PersistentVolumeReclaimPolicy
}

// newClaim returns a copy of the held claim, named name, that can be created as a new object.
func newClaim(pvc *corev1.PersistentVolumeClaim, name string) *corev1.PersistentVolumeClaim {
	n := pvc.DeepCopy()
	n.Name = name
	n.UID = ""
	n.ResourceVersion = ""
	n.Generation = 0
	n.CreationTimestamp = metav1.Time{}
	n.DeletionTimestamp = nil
	n.DeletionGracePeriodSeconds = nil
	n.ManagedFields = nil
	n.Finalizers = slices.DeleteFunc(n.Finalizers, func(f string) bool { return f == Finalizer || f == legacyFinalizer })

	delete(n.Annotations, annHeldVolume)
	delete(n.Annotations, annBindCompleted)
	delete(n.Annotations, annBoundByController)
	delete(n.Annotations, csi.DriverName+"/migrate")
	delete(n.Annotations, csi.DriverName+"/migrate-node")

	if n.Annotations == nil {
		n.Annotations = map[string]string{}
	}

	n.Annotations[AnnRebindFrom] = string(pvc.UID)

	n.Status = corev1.PersistentVolumeClaimStatus{}

	if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
		n.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: capacity}
	}

	return n
}

// newVolume returns a copy of the held volume with the target's topology, reserved for the
// claim named claimName.
func newVolume(pv *corev1.PersistentVolume, policy corev1.PersistentVolumeReclaimPolicy, namespace, claimName string, t Target) *corev1.PersistentVolume {
	n := pv.DeepCopy()
	n.UID = ""
	n.ResourceVersion = ""
	n.Generation = 0
	n.CreationTimestamp = metav1.Time{}
	n.DeletionTimestamp = nil
	n.DeletionGracePeriodSeconds = nil
	n.ManagedFields = nil

	delete(n.Annotations, csi.DriverName+"/migrate")
	delete(n.Annotations, csi.DriverName+"/migrate-node")

	if n.Annotations == nil {
		n.Annotations = map[string]string{}
	}

	n.Annotations[AnnRebindFrom] = string(pv.UID)

	// The volume is created retained, and its policy restored once every claim of the Rebind
	// has its volume reserved.
	n.Annotations[AnnReclaimPolicy] = string(policy)
	n.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
	// Reserve the volume for its claim instead of leaving it unclaimed, where any other
	// matching Pending claim could bind it first.
	n.Spec.ClaimRef = claimRef(namespace, claimName, "")

	if t.VolumeHandle != "" {
		n.Spec.CSI.VolumeHandle = t.VolumeHandle
	}

	if t.NodeAffinity != nil {
		n.Spec.NodeAffinity = t.NodeAffinity.DeepCopy()
	}

	n.Status = corev1.PersistentVolumeStatus{}

	return n
}
