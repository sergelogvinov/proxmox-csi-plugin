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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	rbacv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

// rebindTimeout is the default timeout of rename and swap, in seconds.
const rebindTimeout = 600

// pvcProtectionFinalizer is held by Kubernetes while a scheduled pod uses a claim. The kubelet
// refuses to start a pod with a Terminating claim only once this finalizer is gone.
const pvcProtectionFinalizer = "kubernetes.io/pvc-protection"

// annReboundAt is set on the StatefulSets of a recreated claim, so the StatefulSet controller
// retries them without waiting for its backoff to expire.
const annReboundAt = csi.DriverName + "/rebound-at"

// triggerAttempts bounds how many times triggerStatefulSets triggers the StatefulSets.
const triggerAttempts = 5

// vacateInterval is how often vacate checks the held claims again, and triggerInterval how
// long triggerStatefulSets waits for a pod after each trigger.
var (
	vacateInterval  = 2 * time.Second
	triggerInterval = 5 * time.Second
)

// abortTimeout bounds the Abort of a command that did not finish.
const abortTimeout = 2 * time.Minute

// aborter is the part of a rebind.Rebind that abortHeld needs.
type aborter interface {
	Abort(ctx context.Context) error
}

// abortHeld aborts the Rebind, retrying once. A claim still in use or a Commit that has
// started will not change on a retry, so neither is retried; an Abort that also failed for
// another reason is.
func abortHeld(ctx context.Context, held aborter) error {
	err := held.Abort(ctx)
	if err != nil && !onlyInUse(err) && !errors.Is(err, rebind.ErrCommitted) {
		logger.Warnf("failed to abort the hold on persistentvolumeclaims, retrying: %v", err)

		err = held.Abort(ctx)
	}

	return err
}

// onlyInUse reports whether err is rebind.ErrInUse, or a join of errors that all are: the
// claims are left held, and a re-run resumes them once their pods are gone. An error that
// also carries another failure is not.
func onlyInUse(err error) bool {
	switch e := err.(type) { //nolint:errorlint // walks the error tree itself
	case nil:
		return false
	case interface{ Unwrap() []error }:
		errs := e.Unwrap()
		if len(errs) == 0 {
			return false
		}

		for _, err := range errs {
			if !onlyInUse(err) {
				return false
			}
		}

		return true
	case interface{ Unwrap() error }:
		return onlyInUse(e.Unwrap())
	default:
		return err == rebind.ErrInUse //nolint:errorlint // a leaf of the walk above
	}
}

// logInUse reports claims a pod still uses, which were left held.
func logInUse(err error, claims, volumes []string) {
	logger.Errorf("persistentvolumeclaims %s are left held: %v. A claim a pod still uses stays Terminating with the %s finalizer, "+
		"and persistentvolumes %s are retained. Run the command again once the pods using it are gone, it resumes the held claim",
		strings.Join(claims, ","), err, rebind.Finalizer, strings.Join(volumes, ","))
}

// holdError reports a failed rebind.Hold. Its rollback can leave claims a pod still uses
// held, and only a re-run releases those.
func holdError(err error, claims, volumes []string) error {
	if errors.Is(err, rebind.ErrInUse) {
		logInUse(err, claims, volumes)
	}

	return fmt.Errorf("failed to hold persistentvolumeclaims: %v", err)
}

// heldClaimRef names a held claim, its volume, and the name it is recreated under.
type heldClaimRef struct {
	Name   string
	UID    types.UID
	Volume string
	// NewName is the name of the recreated claim. Empty keeps the name.
	NewName string
}

func (c heldClaimRef) target() string {
	if c.NewName == "" {
		return c.Name
	}

	return c.NewName
}

// recoverySteps describes how to finish by hand a Rebind of claims that was neither committed
// nor aborted. A claim can be left in any state between held and recreated, so every step
// says how to tell whether it is still to do. All originals are released before any claim is
// created, as the new names can be each other's (a swap).
func recoverySteps(namespace string, claims []heldClaimRef) []string {
	steps := []string{}

	for _, c := range claims {
		if c.target() == c.Name {
			steps = append(steps, fmt.Sprintf(
				"If claim %s with uid %s exists and is not Terminating, it was never deleted: remove the %s finalizer from it, "+
					"restore the reclaim policy recorded in the %s annotation of persistentvolume %s, and skip the steps below for it",
				c.Name, c.UID, rebind.Finalizer, rebind.AnnReclaimPolicy, c.Volume))
		}

		steps = append(steps, fmt.Sprintf(
			"If claim %s with uid %s is still Terminating: once no pod uses it, remove the %s finalizer from it and wait until it is gone",
			c.Name, c.UID, rebind.Finalizer))
	}

	for _, c := range claims {
		steps = append(steps,
			fmt.Sprintf("If claim %s exists with the annotation %s=%s, it was already recreated; if no claim %s exists, create it from its manifest below. "+
				"If a claim %s exists without that annotation, it is not part of this run: stop and resolve it by hand",
				c.target(), rebind.AnnRebindFrom, c.UID, c.target(), c.target()),
			fmt.Sprintf("Then reserve persistentvolume %s for claim %s, even if the claim already exists: "+
				"set its spec.claimRef to namespace %s, name %s and the uid of the claim",
				c.Volume, c.target(), namespace, c.target()),
		)
	}

	steps = append(steps, fmt.Sprintf(
		"Once every claim is Bound, restore the reclaim policy recorded in the %s annotation of each persistentvolume",
		rebind.AnnReclaimPolicy))

	return steps
}

// logRecovery logs the recovery steps for claims and prints the manifests of their claims.
func logRecovery(namespace string, held *rebind.Rebind, claims []heldClaimRef) {
	for i, step := range recoverySteps(namespace, claims) {
		logger.Errorf("  %d. %s", i+1, step)
	}

	targets := make([]rebind.Target, 0, len(claims))
	for _, c := range claims {
		targets = append(targets, rebind.Target{Claim: c.Name, NewName: c.NewName})
	}

	printManifests(held, targets...)
}

// abortAndReport aborts the Rebind of a command that did not finish and logs what is left to
// do by hand. claims are the held claims with their targets; a Commit that failed part way
// is reported with them, an Abort that failed with the claims as they were. It reports whether
// the Abort put back the claims it could: all of them, or all but those a pod still uses,
// which are left held. The Abort runs on a context of its own, as ctx may be what failed.
func abortAndReport(ctx context.Context, namespace string, held *rebind.Rebind, claims []heldClaimRef, command string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()

	names := make([]string, 0, len(claims))
	volumes := make([]string, 0, len(claims))

	for _, c := range claims {
		names = append(names, c.Name)
		volumes = append(volumes, c.Volume)
	}

	err := abortHeld(ctx, held)

	switch {
	case err == nil:
		logger.Infof("hold on persistentvolumeclaims %s has been aborted", strings.Join(names, ","))

		return true
	case errors.Is(err, rebind.ErrCommitted):
		logger.Errorf("%s of persistentvolumeclaims %s failed part way, persistentvolumes %s are retained. "+
			"Do not run the %s again. To finish it by hand:", command, strings.Join(names, ","), strings.Join(volumes, ","), command)

		logRecovery(namespace, held, claims)
	case onlyInUse(err):
		// The claims still exist and are held, so recreating them from a manifest would fail.
		logInUse(err, names, volumes)

		// The claims no pod uses were put back.
		return true
	default:
		logger.Errorf("failed to abort the hold on persistentvolumeclaims %s: %v. Persistentvolumes %s are retained. "+
			"To put the claims back by hand:", strings.Join(names, ","), err, strings.Join(volumes, ","))

		original := make([]heldClaimRef, 0, len(claims))
		for _, c := range claims {
			original = append(original, heldClaimRef{Name: c.Name, UID: c.UID, Volume: c.Volume})
		}

		logRecovery(namespace, held, original)
	}

	return false
}

// printManifests prints the objects that finish the Rebind for the targets by hand.
func printManifests(held *rebind.Rebind, targets ...rebind.Target) {
	objs, err := held.Manifests(targets...)
	if err != nil {
		logger.Errorf("failed to build recovery manifests: %v", err)

		return
	}

	for _, obj := range objs {
		manifest, err := json.Marshal(obj)
		if err != nil {
			logger.Errorf("failed to marshal manifest: %v", err)

			continue
		}

		fmt.Println(string(manifest))
	}
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

// commandTimeout converts a --timeout flag in seconds to a duration. Zero or less is def.
func commandTimeout(seconds, def int) (time.Duration, error) {
	if seconds <= 0 {
		seconds = def
	}

	if int64(seconds) > math.MaxInt64/int64(time.Second) {
		return 0, fmt.Errorf("--timeout %d is too large", seconds)
	}

	return time.Duration(seconds) * time.Second, nil
}

// vacated is what vacate saw of the pods that used the held claims.
type vacated struct {
	// Nodes are the nodes of every pod seen using a claim.
	Nodes []string
	// StatefulSets maps each StatefulSet controlling a pod seen using a claim to the claims
	// its pods used.
	StatefulSets map[string][]string
}

// vacate ends every use of the held claims. Pods found using a claim are deleted in force
// mode, and stop it otherwise. It returns once no pod uses any of the claims and Kubernetes
// no longer protects them: the kubelet refuses to start a pod with a Terminating claim only
// once pvcProtectionFinalizer is gone, and no finalizer can be added back to a Terminating
// object, so from then on the claims can be committed and their disks moved. Nodes are not
// cordoned, the Hold alone keeps new pods from starting.
//
// claims are the claims as they were held; a claim replaced since is an error. What was seen
// is returned even on error, as deleted pods cannot be looked up afterwards.
func vacate(ctx context.Context, kube clientkubernetes.Interface, namespace string, claims []*corev1.PersistentVolumeClaim, force bool) (vacated, error) {
	v := vacated{StatefulSets: map[string][]string{}}

	// Keyed by UID: a pod recreated under the same name has to be deleted again.
	deleted := map[types.UID]bool{}

	for {
		inUse := []string{}
		protected := []string{}
		pods := []corev1.Pod{}

		for _, claim := range claims {
			live, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claim.Name, metav1.GetOptions{})
			if err != nil {
				return v, fmt.Errorf("failed to get pvc %s: %v", claim.Name, err)
			}

			if live.UID != claim.UID {
				return v, fmt.Errorf("persistentvolumeclaims %s was replaced while it was held", claim.Name)
			}

			if slices.Contains(live.Finalizers, pvcProtectionFinalizer) {
				protected = append(protected, claim.Name)
			}

			claimPods, err := tools.PVCPods(ctx, kube, namespace, claim.Name)
			if err != nil {
				return v, fmt.Errorf("failed to find pods using pvc %s: %v", claim.Name, err)
			}

			// Recorded right away, so it is returned even if a later claim fails.
			_, nodes, owners := podUsage(claim.Name, claimPods)
			v.Nodes = mergeNames(v.Nodes, nodes...)
			mergeOwners(v.StatefulSets, owners)

			if len(claimPods) > 0 {
				inUse = append(inUse, claim.Name)
			}

			for _, pod := range claimPods {
				if !slices.ContainsFunc(pods, func(p corev1.Pod) bool { return p.UID == pod.UID }) {
					pods = append(pods, pod)
				}
			}
		}

		if len(pods) == 0 && len(protected) == 0 {
			return v, nil
		}

		names := make([]string, 0, len(pods))
		for _, pod := range pods {
			names = append(names, pod.Name)
		}

		if len(pods) > 0 && !force {
			return v, fmt.Errorf("persistentvolumeclaims %s started being used by pods: %s while it was held, re-run with --force",
				strings.Join(inUse, ","), strings.Join(names, ","))
		}

		for _, pod := range pods {
			if deleted[pod.UID] {
				continue
			}

			logger.Infof("persistentvolumeclaims is using by pod %s on node %s, terminating it", pod.Name, pod.Spec.NodeName)

			// The UID precondition keeps a pod that took the name meanwhile from being deleted:
			// the next pass lists it again and checks what it uses.
			err := kube.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{Preconditions: metav1.NewUIDPreconditions(string(pod.UID))})
			if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
				return v, fmt.Errorf("failed to delete pod %s: %v", pod.Name, err)
			}

			deleted[pod.UID] = true
		}

		if len(pods) > 0 {
			logger.Infof("waiting pods: %s", strings.Join(names, " "))
		} else {
			logger.Infof("waiting for persistentvolumeclaims %s to be released", strings.Join(protected, ","))
		}

		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(vacateInterval):
		}
	}
}

// podUsage returns the names and the nodes of the pods using the claim, and the StatefulSets
// controlling them, each mapped to the claim.
func podUsage(claim string, pods []corev1.Pod) (names, nodes []string, owners map[string][]string) {
	owners = map[string][]string{}

	for _, pod := range pods {
		names = append(names, pod.Name)

		if pod.Spec.NodeName != "" {
			nodes = mergeNames(nodes, pod.Spec.NodeName)
		}

		if sts := statefulSetOwner(&pod); sts != "" {
			owners[sts] = mergeNames(owners[sts], claim)
		}
	}

	return names, nodes, owners
}

// statefulSetOwner returns the name of the StatefulSet controlling the pod, if any.
func statefulSetOwner(pod *corev1.Pod) string {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "StatefulSet" {
		return ""
	}

	return owner.Name
}

// mergeOwners adds the claims of each StatefulSet in src to dst.
func mergeOwners(dst, src map[string][]string) {
	for sts, claims := range src {
		dst[sts] = mergeNames(dst[sts], claims...)
	}
}

// mergeNames adds the names that are not in list yet.
func mergeNames(list []string, names ...string) []string {
	for _, name := range names {
		if !slices.Contains(list, name) {
			list = append(list, name)
		}
	}

	return list
}

// triggerStatefulSets annotates the StatefulSets whose pods used the claims, given as each
// StatefulSet mapped to the claims its pods used. A StatefulSet that failed to create its pod
// while a claim was held backs off, and may wait minutes before it notices the recreated
// claim. An update of the object requeues it right away. Only the metadata is changed, which
// does not roll out its pods. The controller may handle the update before its cache has the
// recreated claim, and back off again, so a StatefulSet is triggered with a new annotation
// value until it has a pod using each of its claims again, for a bounded number of attempts.
// A StatefulSet one of whose claims is not in place, still held or gone, is not triggered: its
// pod cannot start anyway. A failure here only delays the pod start and is just logged. It
// runs with its own timeout, as the given context may be about to expire after a long wait.
func triggerStatefulSets(ctx context.Context, kube clientkubernetes.Interface, namespace string, owners map[string][]string) {
	if len(owners) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()

	pending := maps.Clone(owners)

	for attempt := 1; attempt <= triggerAttempts; attempt++ {
		if err := settleStatefulSets(ctx, kube, namespace, pending); err != nil {
			logger.Warnf("failed to check pods of statefulsets: %v", err)
		}

		if len(pending) == 0 {
			return
		}

		// Each attempt needs a new value, an unchanged object is not an update.
		patch := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:%q}}}`, annReboundAt, time.Now().UTC().Format(time.RFC3339Nano))

		for _, name := range slices.Sorted(maps.Keys(pending)) {
			logger.Infof("triggering statefulset %s (attempt %d/%d)", name, attempt, triggerAttempts)

			if _, err := kube.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil && !apierrors.IsNotFound(err) {
				logger.Warnf("failed to trigger statefulset %s: %v", name, err)
			}
		}

		select {
		case <-ctx.Done():
			logger.Warnf("pods of statefulsets %s were not created: %v", strings.Join(slices.Sorted(maps.Keys(pending)), ","), ctx.Err())

			return
		case <-time.After(triggerInterval):
		}
	}

	if err := settleStatefulSets(ctx, kube, namespace, pending); err != nil {
		logger.Warnf("failed to check pods of statefulsets: %v", err)
	}

	if len(pending) > 0 {
		logger.Warnf("pods of statefulsets %s were not created after %d triggers",
			strings.Join(slices.Sorted(maps.Keys(pending)), ","), triggerAttempts)
	}
}

// settleStatefulSets removes from pending each StatefulSet that needs no trigger anymore: it
// has a pod that is not being deleted using each of its claims, whether scheduled yet or not,
// or one of its claims is not in place.
func settleStatefulSets(ctx context.Context, kube clientkubernetes.Interface, namespace string, pending map[string][]string) error {
	podList, err := kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods: %v", err)
	}

	// created maps a StatefulSet to the claims its live pods use.
	created := map[string][]string{}

	for _, pod := range podList.Items {
		sts := statefulSetOwner(&pod)
		if pod.DeletionTimestamp != nil || sts == "" {
			continue
		}

		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil {
				created[sts] = mergeNames(created[sts], volume.PersistentVolumeClaim.ClaimName)
			}
		}
	}

	for sts, claims := range pending {
		settled := true

		for _, claim := range claims {
			pvc, err := kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claim, metav1.GetOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get pvc %s: %v", claim, err)
			}

			if err != nil || pvc.DeletionTimestamp != nil {
				logger.Infof("persistentvolumeclaims %s is not in place, not triggering statefulset %s", claim, sts)

				settled = true

				break
			}

			if !slices.Contains(created[sts], claim) {
				settled = false
			}
		}

		if settled {
			delete(pending, sts)
		}
	}

	return nil
}
