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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

// PVCResources returns the PersistentVolumeClaim and PersistentVolume resources.
func PVCResources(ctx context.Context, clientset clientkubernetes.Interface, namespace, pvcName string) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolume, error) {
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
func PVCPodUsage(ctx context.Context, clientset clientkubernetes.Interface, namespace, pvcName string) (pods []string, node string, err error) {
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
func PVCPods(ctx context.Context, clientset clientkubernetes.Interface, namespace, pvcName string) ([]corev1.Pod, error) {
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
