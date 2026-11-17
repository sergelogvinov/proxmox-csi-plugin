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

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clientkubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// PVCResources returns the PersistentVolumeClaim and PersistentVolume resources.
func PVCResources(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolume, error) {
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get PersistentVolumeClaims: %v", err)
	}

	pv, err := clientset.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get PersistentVolumes: %v", err)
	}

	return pvc, pv, nil
}

// PVCPodUsage returns the list of pods and the node that are using the specified PersistentVolumeClaim.
//
// Only pods that are Pending and have not yet been assigned a node are excluded: they
// cannot have mounted the volume yet. A Pending pod that is already scheduled to a node
// (e.g. still running init containers or ContainerCreating) may already be using the
// volume, so it must still be counted.
func PVCPodUsage(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string) (pods []string, node string, err error) {
	podList, err := PVCPods(ctx, clientset, namespace, pvcName)
	if err != nil {
		return nil, "", err
	}

	for _, pod := range podList {
		pods = append(pods, pod.Name)
		node = pod.Spec.NodeName
	}

	return pods, node, nil
}

// PVCPods returns the pods that use the specified PersistentVolumeClaim, with the same
// selection as PVCPodUsage.
func PVCPods(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string) ([]corev1.Pod, error) {
	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	pods := []corev1.Pod{}

	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodPending && pod.Spec.NodeName == "" {
			continue
		}

		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				pods = append(pods, pod)

				break
			}
		}
	}

	return pods, nil
}

// PVCCreateOrUpdate creates or updates the specified PersistentVolumeClaim resource.
func PVCCreateOrUpdate(
	ctx context.Context,
	clientset *clientkubernetes.Clientset,
	pvc *corev1.PersistentVolumeClaim,
) (*corev1.PersistentVolumeClaim, error) {
	res, err := clientset.CoreV1().PersistentVolumeClaims(pvc.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		patch := corev1.PersistentVolumeClaim{
			Spec: corev1.PersistentVolumeClaimSpec{
				VolumeName: pvc.Spec.VolumeName,
			},
		}

		patchBytes, err := json.Marshal(&patch)
		if err != nil {
			return nil, fmt.Errorf("failed to json.Marshal PVC: %w", err)
		}

		return clientset.CoreV1().PersistentVolumeClaims(pvc.Namespace).Patch(ctx, pvc.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	}

	return res, err
}

// PVReserveForClaim sets a PersistentVolume's claimRef so that it can only be bound by the
// named PersistentVolumeClaim, instead of being left free for any matching Pending claim to
// acquire. The claimRef is replaced wholesale (rather than merged) so that no stale uid or
// resourceVersion from a previous, now-deleted claim survives to block the new claim's bind.
func PVReserveForClaim(ctx context.Context, clientset *clientkubernetes.Clientset, pvName, namespace, claimName string) error {
	// "add" is used rather than "replace": per RFC 6902 it replaces an existing member's
	// value too, but unlike "replace" it does not fail if claimRef happens to be absent.
	patch := fmt.Appendf(nil,
		`[{"op":"add","path":"/spec/claimRef","value":{"kind":"PersistentVolumeClaim","apiVersion":"v1","namespace":%q,"name":%q}}]`,
		namespace, claimName,
	)

	if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, pvName, types.JSONPatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to reserve PersistentVolume %s for claim %s/%s: %v", pvName, namespace, claimName, err)
	}

	return nil
}

// PVSetReclaimPolicy sets the reclaim policy of the specified PersistentVolume.
func PVSetReclaimPolicy(ctx context.Context, clientset *clientkubernetes.Clientset, pvName string, policy corev1.PersistentVolumeReclaimPolicy) error {
	patch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + policy + `"}}`)

	if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, pvName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to set PersistentVolume %s reclaim policy to %s: %v", pvName, policy, err)
	}

	return nil
}

// PVCAddFinalizer adds the finalizer to the specified PersistentVolumeClaim if it is not present yet.
// It fails if the claim is no longer the object with the given UID.
func PVCAddFinalizer(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string, uid types.UID, finalizer string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if pvc.UID != uid {
			return fmt.Errorf("PersistentVolumeClaim %s was replaced, uid %s instead of %s", pvcName, pvc.UID, uid)
		}

		if slices.Contains(pvc.Finalizers, finalizer) {
			return nil
		}

		pvc.Finalizers = append(pvc.Finalizers, finalizer)

		_, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("failed to add finalizer %s to PersistentVolumeClaim %s: %v", finalizer, pvcName, err)
	}

	return nil
}

// PVCRemoveFinalizer removes the finalizer from the specified PersistentVolumeClaim.
// A claim that no longer exists, or that is no longer the object with the given UID, is
// left untouched and is not an error.
func PVCRemoveFinalizer(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string, uid types.UID, finalizer string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}

			return err
		}

		if pvc.UID != uid || !slices.Contains(pvc.Finalizers, finalizer) {
			return nil
		}

		pvc.Finalizers = slices.DeleteFunc(pvc.Finalizers, func(f string) bool { return f == finalizer })

		_, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("failed to remove finalizer %s from PersistentVolumeClaim %s: %v", finalizer, pvcName, err)
	}

	return nil
}

// PVCWaitDelete waits for the specified PersistentVolumeClaim to be deleted.
// Callers that are about to create a new claim with a name that was just deleted (e.g. during
// a swap) must wait for the old object to be fully gone first: a finalizer can keep it around
// after the delete call returns, and creating a same-named claim while it still exists fails.
// With a non-empty uid, a claim with the same name but another UID counts as deleted: the
// object that was waited for is gone, and the caller is left to handle the name collision.
func PVCWaitDelete(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string, uid types.UID) error {
	gone := func() (bool, error) {
		pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}

			return false, err
		}

		return uid != "" && pvc.UID != uid, nil
	}

	if done, err := gone(); err != nil || done {
		return err
	}

	watcher, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + pvcName,
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(10 * time.Minute)

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed unexpectedly")
			}

			if event.Type == watch.Deleted {
				return nil
			}

		case <-ticker.C:
			if done, _ := gone(); done { //nolint: errcheck
				return nil
			}

		case <-timeout:
			return fmt.Errorf("timeout waiting for PersistentVolumeClaim %s to be deleted", pvcName)
		}
	}
}

// PVWaitDelete waits for the specified PersistentVolume to be deleted.
func PVWaitDelete(ctx context.Context, clientset *clientkubernetes.Clientset, pvName string) error {
	// We reuse PV name for change nodeSelector, but some comtrollers may modify PV on deletion event.
	// So we need to wait after PV deletion before creating new PV with the same name.
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	if _, err := clientset.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{}); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	watcher, err := clientset.CoreV1().PersistentVolumes().Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + pvName,
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	timeout := time.After(10 * time.Minute)

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed unexpectedly")
			}

			if event.Type == watch.Deleted {
				return nil
			}

		case <-ticker.C:
			if _, err := clientset.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{}); err != nil {
				if errors.IsNotFound(err) {
					return nil
				}
			}

		case <-timeout:
			return fmt.Errorf("timeout waiting for PersistentVolume %s to be deleted", pvName)
		}
	}
}
