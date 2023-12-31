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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	clientkubernetes "k8s.io/client-go/kubernetes"
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
func PVCPodUsage(ctx context.Context, clientset *clientkubernetes.Clientset, namespace, pvcName string) (pods []string, node string, err error) {
	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("failed to list pods: %v", err)
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			for _, volume := range pod.Spec.Volumes {
				if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
					pods = append(pods, pod.Name)
					node = pod.Spec.NodeName

					break
				}
			}
		}
	}

	return pods, node, nil
}

// PVWaitDelete waits for the specified PersistentVolume to be deleted.
func PVWaitDelete(ctx context.Context, clientset *clientkubernetes.Clientset, pvName string) error {
	_, err := clientset.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return nil //nolint: nilerr
	}

	watcher, err := clientset.CoreV1().PersistentVolumes().Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + pvName,
	})
	if err != nil {
		return err
	}

	defer watcher.Stop()

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

		case <-timeout:
			return fmt.Errorf("timeout waiting for PersistentVolume %s to be deleted", pvName)
		}
	}
}
