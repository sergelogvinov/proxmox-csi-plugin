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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// AnnReboundAt is set on the StatefulSets of the pods that used a held claim, so the
// StatefulSet controller retries them without waiting for its backoff to expire.
const AnnReboundAt = csi.DriverName + "/rebound-at"

var (
	// drainInterval is how often Drain checks the pods of the held claims.
	drainInterval = 2 * time.Second

	// triggerAttempts and triggerInterval bound how long TriggerStatefulSets waits for a pod
	// using a claim to be created, triggerTimeout bounds the whole call.
	triggerAttempts = 5
	triggerInterval = 5 * time.Second
	triggerTimeout  = time.Minute
)

// Drain waits until no pod can use a held claim anymore, so the disk behind it is safe to
// detach and Commit can run. The kubelet refuses to start a pod with a Terminating claim only
// once Kubernetes has released kubernetes.io/pvc-protection, and no finalizer can be added back
// to a Terminating object, so from then on no pod can use the claim.
//
// Scheduled pods using a held claim are deleted when force is set. Otherwise Drain returns an
// error wrapping ErrInUse as soon as it sees one, and changes nothing. A Pending pod that is not
// scheduled yet cannot start with a Terminating claim, and a finished pod no longer uses it:
// both are left alone.
//
// Drain returns the nodes the pods using the claims were seen on, which may still have the
// disk attached. It records the StatefulSets of every pod seen referencing a held claim, for
// TriggerStatefulSets. It fails if a held claim is deleted or replaced while it waits.
func (h *Rebind) Drain(ctx context.Context, force bool) ([]string, error) {
	nodes := []string{}
	deleted := map[types.UID]bool{}

	for {
		inUse := false

		for _, c := range h.claims {
			live, err := h.liveClaim(ctx, c)
			if err != nil {
				return nodes, err
			}

			if live == nil {
				return nodes, fmt.Errorf("rebind: claim %s was deleted or replaced while draining", c.pvc.Name)
			}

			if slices.Contains(live.Finalizers, pvcProtectionFinalizer) {
				inUse = true
			}
		}

		pods, err := h.claimPods(ctx)
		if err != nil {
			return nodes, err
		}

		scheduled := []corev1.Pod{}

		for _, pod := range pods {
			h.recordStatefulSet(&pod)

			// Neither a pod that is not scheduled yet nor one that has finished can use the claim.
			if pod.Spec.NodeName == "" || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}

			scheduled = append(scheduled, pod)

			if !slices.Contains(nodes, pod.Spec.NodeName) {
				nodes = append(nodes, pod.Spec.NodeName)
			}
		}

		if len(scheduled) == 0 && !inUse {
			return nodes, nil
		}

		if len(scheduled) > 0 && !force {
			return nodes, fmt.Errorf("rebind: claims %s are used by pods %s: %w",
				strings.Join(h.claimNames(), ","), strings.Join(podNames(scheduled), ","), ErrInUse)
		}

		for _, pod := range scheduled {
			if deleted[pod.UID] {
				continue
			}

			h.logf("terminating pod %s on node %s", pod.Name, pod.Spec.NodeName)

			err = h.kube.CoreV1().Pods(h.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &pod.UID},
			})
			if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
				return nodes, fmt.Errorf("rebind: delete pod %s: %w", pod.Name, err)
			}

			deleted[pod.UID] = true
		}

		if len(scheduled) > 0 {
			h.logf("waiting for pods: %s", strings.Join(podNames(scheduled), ","))
		} else {
			h.logf("waiting for claims %s to be released", strings.Join(h.claimNames(), ","))
		}

		select {
		case <-ctx.Done():
			return nodes, ctx.Err()
		case <-time.After(drainInterval):
		}
	}
}

// TriggerStatefulSets annotates the StatefulSets recorded by Drain with AnnReboundAt, after
// Commit or Abort. A StatefulSet that failed to create its pod while the claim was held backs
// off, and may wait minutes before it notices the claim is back. An update of the object
// requeues it right away, and as only its metadata changes, its pods are not rolled out.
//
// The controller may handle the update before its cache has the recreated claim, and back off
// again, so the trigger is repeated with a new value until a pod referencing each claim the
// StatefulSets used is created, for a bounded number of attempts. It runs with its own timeout,
// as the given context may be about to expire. It returns an error if no such pod was created.
func (h *Rebind) TriggerStatefulSets(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), triggerTimeout)
	defer cancel()

	var errs []error

	for attempt := 1; ; attempt++ {
		pending, err := h.claimsWithoutPod(ctx)
		if err != nil {
			errs = append(errs, err)
		}

		if err == nil && len(pending) == 0 {
			return nil
		}

		if attempt > triggerAttempts {
			break
		}

		// Each attempt needs a new value, an unchanged object is not an update.
		patch := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:%q}}}`, AnnReboundAt, time.Now().UTC().Format(time.RFC3339Nano))

		for _, name := range h.statefulSetsOf(pending) {
			h.logf("triggering statefulset %s (attempt %d/%d)", name, attempt, triggerAttempts)

			_, err = h.kube.AppsV1().StatefulSets(h.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("rebind: trigger statefulset %s: %w", name, err))
			}
		}

		select {
		case <-ctx.Done():
			return errors.Join(append(errs, fmt.Errorf("rebind: no pod was created for claims %s: %w", strings.Join(pending, ","), ctx.Err()))...)
		case <-time.After(triggerInterval):
		}
	}

	pending, _ := h.claimsWithoutPod(ctx) //nolint: errcheck

	return errors.Join(append(errs, fmt.Errorf("rebind: no pod was created for claims %s after %d triggers of statefulsets %s",
		strings.Join(pending, ","), triggerAttempts, strings.Join(h.statefulSetsOf(pending), ",")))...)
}

// claimsWithoutPod returns the claims with recorded StatefulSets that no pod references yet,
// counting pods that are not scheduled yet but not pods being deleted. A pod references a claim
// by the name it had when it was held: a StatefulSet always uses the name from its template.
func (h *Rebind) claimsWithoutPod(ctx context.Context) ([]string, error) {
	pods, err := h.claimPods(ctx)
	if err != nil {
		return nil, err
	}

	pending := []string{}

	for _, c := range h.claims {
		if len(c.statefulSets) == 0 {
			continue
		}

		found := slices.ContainsFunc(pods, func(pod corev1.Pod) bool {
			return pod.DeletionTimestamp == nil && slices.Contains(podClaims(&pod), c.pvc.Name)
		})

		if !found {
			pending = append(pending, c.pvc.Name)
		}
	}

	return pending, nil
}

// statefulSetsOf returns the StatefulSets recorded for the named claims.
func (h *Rebind) statefulSetsOf(claims []string) []string {
	names := []string{}

	for _, c := range h.claims {
		if !slices.Contains(claims, c.pvc.Name) {
			continue
		}

		for _, name := range c.statefulSets {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}

	return names
}

// claimPods returns the pods referencing a held claim by name.
func (h *Rebind) claimPods(ctx context.Context) ([]corev1.Pod, error) {
	list, err := h.kube.CoreV1().Pods(h.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("rebind: list pods: %w", err)
	}

	names := h.claimNames()
	pods := []corev1.Pod{}

	for _, pod := range list.Items {
		if slices.ContainsFunc(podClaims(&pod), func(name string) bool { return slices.Contains(names, name) }) {
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

// recordStatefulSet records the StatefulSet controlling the pod, if any, on the held claims
// the pod references.
func (h *Rebind) recordStatefulSet(pod *corev1.Pod) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "StatefulSet" {
		return
	}

	claims := podClaims(pod)

	for _, c := range h.claims {
		if slices.Contains(claims, c.pvc.Name) && !slices.Contains(c.statefulSets, owner.Name) {
			c.statefulSets = append(c.statefulSets, owner.Name)
		}
	}
}

func (h *Rebind) claimNames() []string {
	names := make([]string, 0, len(h.claims))
	for _, c := range h.claims {
		names = append(names, c.pvc.Name)
	}

	return names
}

func (h *Rebind) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	}
}

// podClaims returns the names of the claims the pod mounts.
func podClaims(pod *corev1.Pod) []string {
	names := []string{}

	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			names = append(names, v.PersistentVolumeClaim.ClaimName)
		}
	}

	return names
}

func podNames(pods []corev1.Pod) []string {
	names := make([]string, 0, len(pods))
	for _, pod := range pods {
		names = append(names, pod.Name)
	}

	return names
}
