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
	"strings"

	cobra "github.com/spf13/cobra"

	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	rbacv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

type swapCmd struct {
	kclient   *clientkubernetes.Clientset
	namespace string
}

func buildSwapCmd() *cobra.Command {
	c := &swapCmd{}

	cmd := cobra.Command{
		Use:           "swap pvc-a pvc-b",
		Aliases:       []string{"sw"},
		Short:         "Swap PersistentVolumes between two PersistentVolumeClaims",
		Args:          cobra.ExactArgs(2),
		PreRunE:       c.swapValidate,
		RunE:          c.runSwap,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	setSwapCmdFlags(&cmd)

	return &cmd
}

func setSwapCmdFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringP("namespace", "n", "", "namespace of the PersistentVolumeClaims")

	flags.BoolP("force", "f", false, "force swap even if the PersistentVolumeClaims is in use")
	flags.Int("timeout", rebindTimeout, "timeout in seconds")
}

// swapClaim is one of the two claims of a Swap.
type swapClaim struct {
	name  string
	pvc   *corev1.PersistentVolumeClaim
	pv    *corev1.PersistentVolume
	pods  []string
	nodes []string
}

// nolint: cyclop, gocyclo
func (c *swapCmd) runSwap(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	seconds, _ := flags.GetInt("timeout") //nolint: errcheck

	timeout, err := commandTimeout(seconds, rebindTimeout)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	claims := make([]swapClaim, 0, 2)
	statefulSets := map[string][]string{}

	for _, name := range args[:2] {
		cl := swapClaim{name: name}

		var err error

		cl.pvc, cl.pv, err = tools.PVCResources(ctx, c.kclient, c.namespace, name)
		if err != nil {
			return fmt.Errorf("failed to get resources: %v", err)
		}

		// Fail fast, before anything is changed. The authoritative check is vacate.
		usedBy, err := tools.PVCPods(ctx, c.kclient, c.namespace, name)
		if err != nil {
			return fmt.Errorf("failed to find pods using pvc: %v", err)
		}

		var owners map[string][]string

		cl.pods, cl.nodes, owners = podUsage(name, usedBy)

		if len(cl.pods) > 0 && !force {
			return fmt.Errorf("persistentvolumeclaims is using by pods: %s on the node %s, cannot swap pvc", strings.Join(cl.pods, ","), strings.Join(cl.nodes, ","))
		}

		mergeOwners(statefulSets, owners)

		claims = append(claims, cl)
	}

	src, dst := claims[0], claims[1]
	names := []string{src.name, dst.name}

	targets := []rebind.Target{
		{Claim: src.name, NewName: dst.name},
		{Claim: dst.name, NewName: src.name},
	}

	logger.Infof("holding persistentvolumeclaims %s,%s", src.name, dst.name)

	held, err := rebind.Hold(ctx, c.kclient, c.namespace, src.name, dst.name)
	if err != nil {
		return holdError(err, names, []string{src.pv.Name, dst.pv.Name})
	}

	done := false

	defer func() {
		if done {
			return
		}

		// The claims no pod still uses are back as they were: restart the pods deleted for them.
		if abortAndReport(ctx, c.namespace, held, []heldClaimRef{
			{Name: src.name, UID: src.pvc.UID, Volume: src.pv.Name, NewName: dst.name},
			{Name: dst.name, UID: dst.pvc.UID, Volume: dst.pv.Name, NewName: src.name},
		}, "swap") {
			triggerStatefulSets(ctx, c.kclient, c.namespace, statefulSets)
		}
	}()

	for _, cl := range claims {
		if len(cl.pods) > 0 {
			logger.Infof("persistentvolumeclaims %s is using by pods: %s on node %s, trying to force swap", cl.name, strings.Join(cl.pods, ","), strings.Join(cl.nodes, ","))
		}
	}

	seen, err := vacate(ctx, c.kclient, c.namespace, []*corev1.PersistentVolumeClaim{src.pvc, dst.pvc}, force)

	// Merged before the error check: pods may have been deleted already.
	mergeOwners(statefulSets, seen.StatefulSets)

	if err != nil {
		return err
	}

	if err = held.Commit(ctx, targets...); err != nil {
		return fmt.Errorf("failed to swap persistentvolumeclaims: %v", err)
	}

	done = true

	logger.Infof("persistentvolumeclaims %s,%s has been swapped", src.name, dst.name)

	// Both names exist again, so the StatefulSets backing off on them can start their pods.
	triggerStatefulSets(ctx, c.kclient, c.namespace, statefulSets)

	return nil
}

// nolint: dupl,goconst
func (c *swapCmd) swapValidate(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()

	namespace, _ := flags.GetString("namespace") //nolint: errcheck

	kclientConfig, namespace, err := tools.BuildConfig(kubeconfig, namespace)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes config: %v", err)
	}

	c.kclient, err = clientkubernetes.NewForConfig(kclientConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	c.namespace = namespace

	accessCheck := []rbacv1.ResourceAttributes{
		{Group: "", Namespace: c.namespace, Resource: "persistentvolumeclaims", Verb: "create"},
		{Group: "", Namespace: c.namespace, Resource: "persistentvolumeclaims", Verb: "delete"},
		{Group: "", Namespace: c.namespace, Resource: "persistentvolumeclaims", Verb: "update"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "create"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "patch"},
		{Group: "", Namespace: c.namespace, Resource: "pods", Verb: "delete"},
		{Group: "apps", Namespace: c.namespace, Resource: "statefulsets", Verb: "patch"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
