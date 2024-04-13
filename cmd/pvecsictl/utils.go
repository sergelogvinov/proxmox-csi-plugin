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

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/volume"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

func replacePVTopology(
	ctx context.Context,
	clientset *clientkubernetes.Clientset,
	namespace string,
	pvc *corev1.PersistentVolumeClaim,
	pv *corev1.PersistentVolume,
	vol *volume.Volume,
	node string,
) error {
	newPVC := pvc.DeepCopy()
	newPVC.ObjectMeta.UID = ""
	newPVC.ObjectMeta.ResourceVersion = ""
	delete(newPVC.ObjectMeta.Annotations, csi.DriverName+"/migrate")
	delete(newPVC.ObjectMeta.Annotations, csi.DriverName+"/migrate-node")
	newPVC.Status = corev1.PersistentVolumeClaimStatus{}
	newPVC.Spec.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: pvc.Status.Capacity[corev1.ResourceStorage],
	}

	newPV := pv.DeepCopy()
	newPV.ObjectMeta.UID = ""
	newPV.ObjectMeta.ResourceVersion = ""
	delete(newPV.ObjectMeta.Annotations, csi.DriverName+"/migrate")
	delete(newPV.ObjectMeta.Annotations, csi.DriverName+"/migrate-node")
	newPV.Spec.ClaimRef = nil
	newPV.Status = corev1.PersistentVolumeStatus{}
	newPV.Spec.CSI.VolumeHandle = volume.NewVolume(vol.Region(), node, vol.Storage(), vol.Disk()).VolumeID()
	newPV.Spec.NodeAffinity.Required = &corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{
			{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      corev1.LabelTopologyRegion,
						Operator: "In",
						Values:   []string{vol.Region()},
					},
					{
						Key:      corev1.LabelTopologyZone,
						Operator: "In",
						Values:   []string{node},
					},
				},
			},
		},
	}

	policy := metav1.DeletePropagationForeground
	if err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return fmt.Errorf("failed to delete PVC: %v", err)
	}

	if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimDelete {
		if err := clientset.CoreV1().PersistentVolumes().Delete(ctx, pv.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
			return fmt.Errorf("failed to delete PV: %v", err)
		}
	}

	if err := tools.PVWaitDelete(ctx, clientset, pv.Name); err != nil {
		return fmt.Errorf("failed to wait for PV deletion: %v", err)
	}

	if _, err := clientset.CoreV1().PersistentVolumes().Create(ctx, newPV, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create PV: %v", err)
	}

	if _, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, newPVC, metav1.CreateOptions{}); err != nil {
		if _, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, newPVC, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to create/update PVC: %v", err)
		}
	}

	return nil
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
	newPVC.ObjectMeta.UID = ""
	newPVC.ObjectMeta.ResourceVersion = ""
	newPVC.Status = corev1.PersistentVolumeClaimStatus{}
	newPVC.Spec.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: pvc.Status.Capacity[corev1.ResourceStorage],
	}

	patch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"` + corev1.PersistentVolumeReclaimRetain + `"}}`)

	if pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimDelete {
		if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, pvc.Spec.VolumeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch PersistentVolumes: %v", err)
		}
	}

	policy := metav1.DeletePropagationForeground
	if err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return fmt.Errorf("failed to delete PersistentVolumeClaims: %v", err)
	}

	patch = []byte(`{"spec":{"claimRef":null}}`)

	if _, err := clientset.CoreV1().PersistentVolumes().Patch(ctx, pvc.Spec.VolumeName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to patch PersistentVolumes: %v", err)
	}

	if _, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, newPVC, metav1.CreateOptions{}); err != nil {
		if _, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, newPVC, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to create/update PersistentVolumeClaims: %v", err)
		}
	}

	return nil
}
