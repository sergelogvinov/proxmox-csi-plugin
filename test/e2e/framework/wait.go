//go:build e2e

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

package framework

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// pollInterval is how often waiters re-check cluster state.
const pollInterval = 2 * time.Second

// WaitForPodReady polls until the named pod's Ready condition is true.
func WaitForPodReady(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, timeout time.Duration) (*corev1.Pod, error) {
	var pod *corev1.Pod

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		p, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		pod = p

		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		return pod, fmt.Errorf("pod %s/%s did not become ready: %w", namespace, name, err)
	}

	return pod, nil
}

// WaitForStatefulSetReplicasReady polls until a StatefulSet reports the given
// number of ready replicas.
func WaitForStatefulSetReplicasReady(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, replicas int32, timeout time.Duration) (*appsv1.StatefulSet, error) {
	var sts *appsv1.StatefulSet

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		s, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		sts = s

		return s.Status.ReadyReplicas == replicas, nil
	})
	if err != nil {
		return sts, fmt.Errorf("statefulset %s/%s did not reach %d ready replicas: %w", namespace, name, replicas, err)
	}

	return sts, nil
}

// WaitForPVCBound polls until the named PersistentVolumeClaim is Bound and
// returns it together with the PersistentVolume it is bound to.
func WaitForPVCBound(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, timeout time.Duration) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolume, error) {
	var pvc *corev1.PersistentVolumeClaim

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		p, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		pvc = p

		return p.Status.Phase == corev1.ClaimBound, nil
	})
	if err != nil {
		return pvc, nil, fmt.Errorf("pvc %s/%s did not become bound: %w", namespace, name, err)
	}

	pv, err := clientset.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		return pvc, nil, fmt.Errorf("failed to get pv %q bound to pvc %s/%s: %w", pvc.Spec.VolumeName, namespace, name, err)
	}

	return pvc, pv, nil
}

// WaitForPVCExists polls until the named PersistentVolumeClaim exists and
// returns it as-is - unlike WaitForPVCBound, it does not wait for the PVC
// to be Bound. Used to observe a PVC's state (e.g. still unbound) right
// after creation, before whatever binds it (a scheduled consumer pod, for
// a WaitForFirstConsumer StorageClass) has had a chance to run.
func WaitForPVCExists(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, timeout time.Duration) (*corev1.PersistentVolumeClaim, error) {
	var pvc *corev1.PersistentVolumeClaim

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		p, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		pvc = p

		return true, nil
	})
	if err != nil {
		return pvc, fmt.Errorf("pvc %s/%s was never created: %w", namespace, name, err)
	}

	return pvc, nil
}

// WaitForPVCResized polls until the PVC's status capacity reaches wantSize
// and it carries no pending resize conditions.
func WaitForPVCResized(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, wantSize resource.Quantity, timeout time.Duration) (*corev1.PersistentVolumeClaim, error) {
	var pvc *corev1.PersistentVolumeClaim

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		p, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		pvc = p

		capacity, ok := p.Status.Capacity[corev1.ResourceStorage]
		if !ok || capacity.Cmp(wantSize) < 0 {
			return false, nil
		}

		for _, cond := range p.Status.Conditions {
			pending := cond.Type == corev1.PersistentVolumeClaimResizing ||
				cond.Type == corev1.PersistentVolumeClaimFileSystemResizePending

			if pending && cond.Status == corev1.ConditionTrue {
				return false, nil
			}
		}

		return true, nil
	})
	if err != nil {
		return pvc, fmt.Errorf("pvc %s/%s did not resize to %s: %w", namespace, name, wantSize.String(), err)
	}

	return pvc, nil
}

// WaitForPVCVolumeAttributesClass polls until the PVC's
// status.currentVolumeAttributesClassName reaches wantClass and no
// ControllerModifyVolume operation is still in flight. An "Infeasible"
// modifyVolumeStatus (the CSI driver rejected the requested parameters) is a
// terminal failure, so the wait aborts immediately instead of polling until
// timeout.
func WaitForPVCVolumeAttributesClass(ctx context.Context, clientset *kubernetes.Clientset, namespace, name, wantClass string, timeout time.Duration) (*corev1.PersistentVolumeClaim, error) {
	var pvc *corev1.PersistentVolumeClaim

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		p, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		pvc = p

		if status := p.Status.ModifyVolumeStatus; status != nil {
			if status.Status == corev1.PersistentVolumeClaimModifyVolumeInfeasible {
				return false, fmt.Errorf("modifying pvc %s/%s to volumeattributesclass %s was rejected as infeasible (target %s)",
					namespace, name, wantClass, status.TargetVolumeAttributesClassName)
			}

			return false, nil
		}

		return p.Status.CurrentVolumeAttributesClassName != nil && *p.Status.CurrentVolumeAttributesClassName == wantClass, nil
	})
	if err != nil {
		return pvc, fmt.Errorf("pvc %s/%s did not converge to volumeattributesclass %s: %w", namespace, name, wantClass, err)
	}

	return pvc, nil
}

// waitForGone polls getErr (a Get call's error) until it reports NotFound.
// A transient error (an API server hiccup) retries; anything else aborts
// the wait immediately instead of silently retrying it until the timeout.
func waitForGone(ctx context.Context, timeout time.Duration, getErr func(context.Context) error) error {
	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		err := getErr(ctx)

		switch {
		case apierrors.IsNotFound(err):
			return true, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		return false, nil
	})
}

// WaitForPodGone polls until the named Pod no longer exists. Used to
// confirm a StatefulSet scale-down actually removed a replica's pod, as
// opposed to WaitForPVCGone/WaitForPVGone: scaling down does *not* delete
// the ordinal's PVC/PV by default, so those must stay around.
func WaitForPodGone(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, timeout time.Duration) error {
	err := waitForGone(ctx, timeout, func(ctx context.Context) error {
		_, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("pod %s/%s was not deleted: %w", namespace, name, err)
	}

	return nil
}

// WaitForPVGone polls until the named PersistentVolume no longer exists.
func WaitForPVGone(ctx context.Context, clientset *kubernetes.Clientset, name string, timeout time.Duration) error {
	err := waitForGone(ctx, timeout, func(ctx context.Context) error {
		_, err := clientset.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("pv %q was not deleted: %w", name, err)
	}

	return nil
}

// WaitForPVCGone polls until the named PersistentVolumeClaim no longer exists.
func WaitForPVCGone(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, timeout time.Duration) error {
	err := waitForGone(ctx, timeout, func(ctx context.Context) error {
		_, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("pvc %s/%s was not deleted: %w", namespace, name, err)
	}

	return nil
}

// WaitForNamespaceGone polls until the named Namespace no longer exists.
func WaitForNamespaceGone(ctx context.Context, clientset *kubernetes.Clientset, name string, timeout time.Duration) error {
	err := waitForGone(ctx, timeout, func(ctx context.Context) error {
		_, err := clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("namespace %q was not deleted: %w", name, err)
	}

	return nil
}
