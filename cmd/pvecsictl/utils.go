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
	"fmt"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	rbacv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

// Binding annotations the PV controller sets on a bound claim. A recreated claim must not carry
// them over: with bind-completed, the controller treats it as already bound and marks it Lost,
// because its PV is missing or reserved without the new claim's UID.
const (
	annBindCompleted     = "pv.kubernetes.io/bind-completed"
	annBoundByController = "pv.kubernetes.io/bound-by-controller"
)

// clearClaimBinding removes the binding annotations from a claim that is about to be recreated,
// so the PV controller binds it to its reserved PV as a new claim.
func clearClaimBinding(pvc *corev1.PersistentVolumeClaim) {
	delete(pvc.ObjectMeta.Annotations, annBindCompleted)
	delete(pvc.ObjectMeta.Annotations, annBoundByController)
}

// triggerStatefulSets gets the StatefulSets of the pods a Rebind drained to create them again
// right away. The claims are already in place, so a failure only delays the pod start.
func triggerStatefulSets(ctx context.Context, held *rebind.Rebind) {
	if err := held.TriggerStatefulSets(ctx); err != nil {
		logger.Warnf("%v", err)
	}
}

func cordoneNodeWithPVs(
	ctx context.Context,
	kclient *clientkubernetes.Clientset,
	pv *corev1.PersistentVolume,
) ([]string, error) {
	csiNodes, err := tools.CSINodes(ctx, kclient, pv.Spec.CSI.Driver)
	if err != nil {
		return nil, err
	}

	// Return whatever was actually cordoned, even on error, so callers can still
	// uncordon exactly those nodes instead of losing track of partial progress.
	cordonedNodes, err := tools.CondonNodes(ctx, kclient, csiNodes)

	return cordonedNodes, err
}

func renamePVC(
	ctx context.Context,
	clientset *clientkubernetes.Clientset,
	namespace string,
	pvc *corev1.PersistentVolumeClaim,
	pv *corev1.PersistentVolume,
	newName string,
) error {
	newPVC := pvc.DeepCopy()
	newPVC.ObjectMeta.Name = newName
	clearClaimBinding(newPVC)
	newPVC.ObjectMeta.UID = ""
	newPVC.ObjectMeta.ResourceVersion = ""
	newPVC.Status = corev1.PersistentVolumeClaimStatus{}
	newPVC.Spec.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: pvc.Status.Capacity[corev1.ResourceStorage],
	}

	originalPolicy := pv.Spec.PersistentVolumeReclaimPolicy
	patch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + corev1.PersistentVolumeReclaimRetain + `"}}`)

	if originalPolicy == corev1.PersistentVolumeReclaimDelete {
		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, pvc.Spec.VolumeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch PersistentVolume: %v", err)
		}
	}

	policy := metav1.DeletePropagationForeground
	if err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return fmt.Errorf("failed to delete PersistentVolumeClaim: %v", err)
	}

	// Reserve the PV for newName instead of clearing claimRef entirely, so no other
	// Pending claim can bind to it while the renamed claim is being created.
	if err := tools.PVReserveForClaim(ctx, clientset, pvc.Spec.VolumeName, namespace, newName); err != nil {
		return err
	}

	// The destination name was already validated as free before this function was
	// called, so a plain Create is used: falling back to updating whatever object
	// currently holds that name would risk mutating an unrelated claim.
	if _, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, newPVC, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create PersistentVolumeClaim %s: %v", newPVC.Name, err)
	}

	if originalPolicy == corev1.PersistentVolumeReclaimDelete {
		restorePatch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + corev1.PersistentVolumeReclaimDelete + `"}}`)

		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, pvc.Spec.VolumeName, types.MergePatchType, restorePatch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to restore PersistentVolume reclaim policy: %v", err)
		}
	}

	return nil
}

func swapPVC(
	ctx context.Context,
	clientset *clientkubernetes.Clientset,
	namespace string,
	srcPVC *corev1.PersistentVolumeClaim,
	srcPV *corev1.PersistentVolume,
	dstPVC *corev1.PersistentVolumeClaim,
	dstPV *corev1.PersistentVolume,
) error {
	newSrcPVC := srcPVC.DeepCopy()
	newSrcPVC.ObjectMeta.Name = dstPVC.ObjectMeta.Name
	clearClaimBinding(newSrcPVC)
	newSrcPVC.ObjectMeta.UID = ""
	newSrcPVC.ObjectMeta.ResourceVersion = ""
	newSrcPVC.Status = corev1.PersistentVolumeClaimStatus{}
	newSrcPVC.Spec.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: srcPVC.Status.Capacity[corev1.ResourceStorage],
	}

	newDstPVC := dstPVC.DeepCopy()
	newDstPVC.ObjectMeta.Name = srcPVC.ObjectMeta.Name
	clearClaimBinding(newDstPVC)
	newDstPVC.ObjectMeta.UID = ""
	newDstPVC.ObjectMeta.ResourceVersion = ""
	newDstPVC.Status = corev1.PersistentVolumeClaimStatus{}
	newDstPVC.Spec.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: dstPVC.Status.Capacity[corev1.ResourceStorage],
	}

	originalSrcPolicy := srcPV.Spec.PersistentVolumeReclaimPolicy
	originalDstPolicy := dstPV.Spec.PersistentVolumeReclaimPolicy

	patch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + corev1.PersistentVolumeReclaimRetain + `"}}`)

	if originalSrcPolicy == corev1.PersistentVolumeReclaimDelete {
		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, srcPVC.Spec.VolumeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch PersistentVolume: %v", err)
		}
	}

	if originalDstPolicy == corev1.PersistentVolumeReclaimDelete {
		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, dstPVC.Spec.VolumeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch PersistentVolume: %v", err)
		}
	}

	policy := metav1.DeletePropagationForeground

	if err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, srcPVC.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return fmt.Errorf("failed to delete PersistentVolumeClaim: %v", err)
	}

	if err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, dstPVC.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return fmt.Errorf("failed to delete PersistentVolumeClaim: %v", err)
	}

	// The claims being recreated reuse each other's names (src -> dstPVC.Name and
	// dst -> srcPVC.Name), so a finalizer keeping either deleted claim around a
	// moment longer would make the matching Create below fail with AlreadyExists.
	if err := tools.PVCWaitDelete(ctx, clientset, namespace, srcPVC.Name, ""); err != nil {
		return fmt.Errorf("failed to wait for PersistentVolumeClaim %s deletion: %v", srcPVC.Name, err)
	}

	if err := tools.PVCWaitDelete(ctx, clientset, namespace, dstPVC.Name, ""); err != nil {
		return fmt.Errorf("failed to wait for PersistentVolumeClaim %s deletion: %v", dstPVC.Name, err)
	}

	// Reserve each PV for the claim it will now belong to, so no other Pending
	// claim can bind to it while the swapped claims are being created.
	if err := tools.PVReserveForClaim(ctx, clientset, srcPVC.Spec.VolumeName, namespace, newSrcPVC.Name); err != nil {
		return err
	}

	if err := tools.PVReserveForClaim(ctx, clientset, dstPVC.Spec.VolumeName, namespace, newDstPVC.Name); err != nil {
		return err
	}

	if _, err := tools.PVCCreateOrUpdate(ctx, clientset, newSrcPVC); err != nil {
		return fmt.Errorf("failed to create/update PersistentVolumeClaim %s: %v", newSrcPVC.Name, err)
	}

	if _, err := tools.PVCCreateOrUpdate(ctx, clientset, newDstPVC); err != nil {
		return fmt.Errorf("failed to create/update PersistentVolumeClaim %s: %v", newDstPVC.Name, err)
	}

	if originalSrcPolicy == corev1.PersistentVolumeReclaimDelete {
		restorePatch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + corev1.PersistentVolumeReclaimDelete + `"}}`)

		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, srcPVC.Spec.VolumeName, types.MergePatchType, restorePatch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to restore PersistentVolume reclaim policy: %v", err)
		}
	}

	if originalDstPolicy == corev1.PersistentVolumeReclaimDelete {
		restorePatch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + corev1.PersistentVolumeReclaimDelete + `"}}`)

		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, dstPVC.Spec.VolumeName, types.MergePatchType, restorePatch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to restore PersistentVolume reclaim policy: %v", err)
		}
	}

	return nil
}

// checkClusters probes every configured cluster's connectivity and
// permissions, returning the first error encountered. go-proxmox-pool has
// no pool-level check; each cluster is checked individually via its
// Cluster handle.
func checkClusters(ctx context.Context, pool *pxpool.ProxmoxPool) error {
	for _, name := range pool.List() {
		if err := pool.Cluster(name).Check(ctx); err != nil {
			return err
		}
	}

	return nil
}

func checkPermissions(ctx context.Context, clientset clientkubernetes.Interface, perms []rbacv1.ResourceAttributes) error {
	for _, a := range perms {
		sar := &rbacv1.SelfSubjectAccessReview{
			Spec: rbacv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &a,
			},
		}

		res, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to send SubjectAccessReview request: %v", err)
		}

		if !res.Status.Allowed {
			return fmt.Errorf("rbac: you are not allowed to %s %s", a.Verb, a.Resource)
		}
	}

	return nil
}
