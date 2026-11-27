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
	"errors"
	"fmt"
	"strings"
	"time"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	rbacv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

// defaultRebindTimeout is the default of the --timeout flag of rename and swap, in seconds.
const defaultRebindTimeout = 600

// commandTimeout returns the timeout of a command from its --timeout flag, in seconds.
func commandTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = defaultRebindTimeout
	}

	return time.Duration(seconds) * time.Second
}

// triggerStatefulSets gets the StatefulSets of the pods a Rebind drained to create them again
// right away. The claims are already in place, so a failure only delays the pod start.
func triggerStatefulSets(ctx context.Context, held *rebind.Rebind) {
	if err := held.TriggerStatefulSets(ctx); err != nil {
		logger.Warnf("%v", err)
	}
}

// rebindClaims rebinds the claims to their targets: it holds them, drains them, and commits
// the targets. Pods using the claims are deleted in force mode, and stop the Rebind before
// anything is changed otherwise.
//
// A failure before Commit aborts the Rebind, a failed Commit is retried once. If that fails as
// well, the claims are left held and the state is logged. A re-run of the same command adopts
// claims that are still held.
func rebindClaims(
	ctx context.Context,
	kclient clientkubernetes.Interface,
	namespace string,
	force bool,
	claims []string,
	targets ...rebind.Target,
) (retErr error) {
	names := strings.Join(claims, ",")

	// Fail fast, before anything is changed. Drain does the authoritative check.
	for _, claim := range claims {
		pods, err := tools.PVCPods(ctx, kclient, namespace, claim)
		if err != nil {
			return fmt.Errorf("failed to find pods using persistentvolumeclaims %s: %v", claim, err)
		}

		if len(pods) == 0 {
			continue
		}

		usage := []string{}
		for _, pod := range pods {
			usage = append(usage, pod.Name+" on node "+pod.Spec.NodeName)
		}

		if !force {
			return fmt.Errorf("persistentvolumeclaims %s is used by pods: %s, re-run with --force to terminate them", claim, strings.Join(usage, ","))
		}

		logger.Infof("persistentvolumeclaims %s is used by pods: %s, they will be terminated", claim, strings.Join(usage, ","))
	}

	logger.Infof("holding persistentvolumeclaims %s", names)

	held, err := rebind.Hold(ctx, kclient, namespace, claims...)
	if err != nil {
		return fmt.Errorf("failed to hold persistentvolumeclaims: %v", err)
	}

	held.Logf = logger.Infof
	committing := false

	defer func() {
		if retErr == nil {
			return
		}

		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()

		if !committing {
			logger.Infof("restoring persistentvolumeclaims %s", names)

			err := held.Abort(cleanupCtx)
			if err != nil && !errors.Is(err, rebind.ErrInUse) {
				logger.Warnf("failed to restore persistentvolumeclaims, retrying: %v", err)

				err = held.Abort(cleanupCtx)
			}

			if err == nil {
				triggerStatefulSets(cleanupCtx, held)

				return
			}

			logger.Errorf("failed to restore persistentvolumeclaims %s: %v", names, err)
			logger.Errorf("persistentvolumeclaims %s are left terminating with the %s finalizer and their persistentvolumes are retained, "+
				"re-run the same command to finish it", names, rebind.Finalizer)

			return
		}

		logger.Infof("retrying to recreate persistentvolumeclaims %s", names)

		err := held.Commit(cleanupCtx, targets...)
		if err == nil {
			triggerStatefulSets(cleanupCtx, held)

			retErr = nil

			return
		}

		logger.Errorf("failed to recreate persistentvolumeclaims %s: %v", names, err)
		logger.Errorf("persistentvolumeclaims %s may be partly recreated. Claims and volumes it created are annotated with %s, "+
			"its persistentvolumes are retained and their original reclaim policy is recorded in the %s annotation",
			names, rebind.AnnRebindFrom, rebind.AnnReclaimPolicy)
	}()

	if _, err = held.Drain(ctx, force); err != nil {
		if errors.Is(err, rebind.ErrInUse) {
			return fmt.Errorf("persistentvolumeclaims %s started being used while being held, re-run with --force: %v", names, err)
		}

		return fmt.Errorf("failed to wait for persistentvolumeclaims %s to be released: %v", names, err)
	}

	committing = true

	if err = held.Commit(ctx, targets...); err != nil {
		return fmt.Errorf("failed to recreate persistentvolumeclaims %s: %v", names, err)
	}

	triggerStatefulSets(ctx, held)

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
