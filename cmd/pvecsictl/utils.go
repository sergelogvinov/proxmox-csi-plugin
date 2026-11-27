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

// annMigratedAt is set on the StatefulSets of a migrated claim, so the StatefulSet controller
// retries them without waiting for its backoff to expire.
const annMigratedAt = csi.DriverName + "/migrated-at"

// pvcProtectionFinalizer is held by Kubernetes while a scheduled pod uses a claim. The kubelet
// refuses to start a pod with a Terminating claim only once this finalizer is gone.
const pvcProtectionFinalizer = "kubernetes.io/pvc-protection"

// commitRetries and commitInterval bound how long commitWhenReleased waits for Kubernetes to
// release the held claims once the pods using them are gone.
const (
	commitRetries  = 30
	commitInterval = 2 * time.Second
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

// uniquePods merges pod lists, dropping duplicates: a pod can use more than one claim.
func uniquePods(lists ...[]string) []string {
	pods := []string{}

	for _, list := range lists {
		for _, pod := range list {
			if !slices.Contains(pods, pod) {
				pods = append(pods, pod)
			}
		}
	}

	return pods
}

// deletePods deletes the pods. A pod that is already gone is what the delete is for.
func deletePods(ctx context.Context, kclient clientkubernetes.Interface, namespace string, pods []string) error {
	for _, pod := range pods {
		err := kclient.CoreV1().Pods(namespace).Delete(ctx, pod, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod %s: %v", pod, err)
		}
	}

	return nil
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
// is reported with them, an Abort that failed with the claims as they were. It returns false
// when the claims are left for manual recovery, so nodes cordoned for the command have to
// stay cordoned. The Abort runs on a context of its own, as ctx may be what failed.
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

// commitWhenReleased commits the Rebind. Kubernetes removes kubernetes.io/pvc-protection
// shortly after the last pod using a claim is gone, and Commit refuses to start before, so
// it is retried while a claim is in use.
func commitWhenReleased(ctx context.Context, held *rebind.Rebind, targets ...rebind.Target) error {
	claims := make([]string, 0, len(targets))
	for _, t := range targets {
		claims = append(claims, t.Claim)
	}

	return retryWhileInUse(ctx, commitRetries, commitInterval,
		func(ctx context.Context) error { return held.Commit(ctx, targets...) },
		func() {
			logger.Infof("waiting for persistentvolumeclaims %s to be released", strings.Join(claims, ","))
		},
	)
}

// retryWhileInUse calls commit until it returns something other than rebind.ErrInUse, at most
// retries more times, interval apart. onWait is called before each wait.
func retryWhileInUse(ctx context.Context, retries int, interval time.Duration, commit func(context.Context) error, onWait func()) error {
	for attempt := 0; ; attempt++ {
		err := commit(ctx)
		if !errors.Is(err, rebind.ErrInUse) {
			return err
		}

		if attempt >= retries {
			return fmt.Errorf("%w: still in use after %d attempts", err, attempt+1)
		}

		onWait()

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", err, ctx.Err())
		case <-time.After(interval):
		}
	}
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
