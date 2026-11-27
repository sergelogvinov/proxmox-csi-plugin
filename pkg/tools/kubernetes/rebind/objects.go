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

package rebind

import (
	"context"
	"fmt"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

var (
	// pollInterval is how often deletions are checked for.
	pollInterval = time.Second

	// waitTimeout bounds every wait for a deletion.
	waitTimeout = 10 * time.Minute

	// rollbackTimeout bounds the rollback of a Hold that failed.
	rollbackTimeout = 2 * time.Minute

	// volumeSettleDelay is waited after a volume is deleted before its name is reused:
	// some controllers still update a volume while handling its deletion.
	volumeSettleDelay = 5 * time.Second
)

// addFinalizer adds Finalizer to the held claim, as long as it is still the object that was read.
func (h *Rebind) addFinalizer(ctx context.Context, c *heldClaim) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pvc, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, c.pvc.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if pvc.UID != c.pvc.UID {
			return fmt.Errorf("claim was replaced, uid %s instead of %s", pvc.UID, c.pvc.UID)
		}

		if IsHeld(pvc) {
			return nil
		}

		pvc.Finalizers = append(pvc.Finalizers, Finalizer)

		if pvc.Annotations == nil {
			pvc.Annotations = map[string]string{}
		}

		pvc.Annotations[annHeldVolume] = string(c.pv.UID)

		_, err = h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Update(ctx, pvc, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("rebind: add finalizer to claim %s: %w", c.pvc.Name, err)
	}

	return nil
}

// releaseInPlace removes the hold from a claim that was never deleted. It reports whether
// the claim turned out to be Terminating after all, in which case it is left held: releasing
// it would let it go away with nothing to recreate it. The update is bound to the version
// read, so a delete that takes effect in between is always seen.
func (h *Rebind) releaseInPlace(ctx context.Context, c *heldClaim) (terminating bool, err error) {
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pvc, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, c.pvc.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if pvc.UID != c.pvc.UID {
			return fmt.Errorf("claim was replaced, uid %s instead of %s", pvc.UID, c.pvc.UID)
		}

		if pvc.DeletionTimestamp != nil {
			terminating = true

			return nil
		}

		if !IsHeld(pvc) {
			return nil
		}

		pvc.Finalizers = slices.DeleteFunc(pvc.Finalizers, func(f string) bool { return f == Finalizer || f == legacyFinalizer })
		delete(pvc.Annotations, annHeldVolume)

		_, err = h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Update(ctx, pvc, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("rebind: release claim %s: %w", c.pvc.Name, err)
	}

	return terminating, nil
}

// removeFinalizers removes Finalizer and the legacy finalizer from the held claim. A claim
// that is gone, or was replaced by another object, is left alone.
func (h *Rebind) removeFinalizers(ctx context.Context, c *heldClaim) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pvc, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, c.pvc.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		if err != nil {
			return err
		}

		if pvc.UID != c.pvc.UID || !IsHeld(pvc) {
			return nil
		}

		pvc.Finalizers = slices.DeleteFunc(pvc.Finalizers, func(f string) bool { return f == Finalizer || f == legacyFinalizer })
		delete(pvc.Annotations, annHeldVolume)

		_, err = h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Update(ctx, pvc, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("rebind: remove finalizer from claim %s: %w", c.pvc.Name, err)
	}

	return nil
}

// waitClaimDeleted waits until the claim with the given UID is gone. A claim with the same
// name but another UID counts as gone.
func (h *Rebind) waitClaimDeleted(ctx context.Context, name string, uid types.UID) error {
	return waitGone(ctx, 0, func(ctx context.Context) (types.UID, error) {
		pvc, err := h.kube.CoreV1().PersistentVolumeClaims(h.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}

		return pvc.UID, nil
	}, uid)
}

// waitVolumeDeleted waits until the volume with the given UID is gone. A volume with the same
// name but another UID counts as gone.
func (h *Rebind) waitVolumeDeleted(ctx context.Context, name string, uid types.UID) error {
	return waitGone(ctx, volumeSettleDelay, func(ctx context.Context) (types.UID, error) {
		pv, err := h.kube.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}

		return pv.UID, nil
	}, uid)
}

func waitGone(ctx context.Context, settle time.Duration, get func(context.Context) (types.UID, error), uid types.UID) error {
	ctx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	for {
		current, err := get(ctx)
		if apierrors.IsNotFound(err) || (err == nil && current != uid) {
			break
		}

		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	if settle == 0 {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settle):
		return nil
	}
}
