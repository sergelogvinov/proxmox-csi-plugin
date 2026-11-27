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
	"time"

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
}

// swapClaim is one of the two claims of a Swap.
type swapClaim struct {
	name string
	pvc  *corev1.PersistentVolumeClaim
	pv   *corev1.PersistentVolume
	pods []string
	node string
}

// nolint: cyclop, gocyclo
func (c *swapCmd) runSwap(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	var err error

	ctx := context.Background()

	claims := make([]swapClaim, 0, 2)

	for _, name := range args[:2] {
		cl := swapClaim{name: name}

		cl.pvc, cl.pv, err = tools.PVCResources(ctx, c.kclient, c.namespace, name)
		if err != nil {
			return fmt.Errorf("failed to get resources: %v", err)
		}

		cl.pods, cl.node, err = tools.PVCPodUsage(ctx, c.kclient, c.namespace, name)
		if err != nil {
			return fmt.Errorf("failed to find pods using pvc: %v", err)
		}

		claims = append(claims, cl)
	}

	src, dst := claims[0], claims[1]

	pods := uniquePods(src.pods, dst.pods)

	if len(pods) > 0 {
		for _, cl := range claims {
			if len(cl.pods) > 0 && !force {
				return fmt.Errorf("persistentvolumeclaims is using by pods: %s on the node %s, cannot swap pvc", strings.Join(cl.pods, ","), cl.node)
			}
		}

		if src.pv.Spec.CSI == nil || dst.pv.Spec.CSI == nil {
			return fmt.Errorf("only CSI PersistentVolumes can be swapped in force mode")
		}
	}

	cordonedNodes := []string{}
	unsafeToUncordon := false

	defer func() {
		if len(cordonedNodes) == 0 {
			return
		}

		if unsafeToUncordon {
			logger.Errorf("swap failed after storage changes started, leaving nodes cordoned for manual recovery: %s", strings.Join(cordonedNodes, ","))

			return
		}

		logger.Infof("uncordoning nodes: %s", strings.Join(cordonedNodes, ","))

		if err := tools.UncondonNodes(ctx, c.kclient, cordonedNodes); err != nil {
			logger.Errorf("failed to uncordon nodes: %v", err)
		}
	}()

	targets := []rebind.Target{
		{Claim: src.name, NewName: dst.name},
		{Claim: dst.name, NewName: src.name},
	}

	logger.Infof("holding persistentvolumeclaims %s,%s", src.name, dst.name)

	held, err := rebind.Hold(ctx, c.kclient, c.namespace, src.name, dst.name)
	if err != nil {
		return holdError(err, []string{src.name, dst.name}, []string{src.pv.Name, dst.pv.Name})
	}

	done := false

	// Runs before the uncordon above, so it can tell whether the nodes are safe to uncordon.
	defer func() {
		if done {
			return
		}

		if !abortAndReport(ctx, c.namespace, held, []heldClaimRef{
			{Name: src.name, UID: src.pvc.UID, Volume: src.pv.Name, NewName: dst.name},
			{Name: dst.name, UID: dst.pvc.UID, Volume: dst.pv.Name, NewName: src.name},
		}, "swap") {
			unsafeToUncordon = true
		}
	}()

	if len(pods) > 0 {
		for _, cl := range claims {
			if len(cl.pods) == 0 {
				continue
			}

			logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force swap\n", strings.Join(cl.pods, ","), cl.node)

			csiNodes, err := cordoneNodeWithPVs(ctx, c.kclient, cl.pv)
			cordonedNodes = append(cordonedNodes, csiNodes...)

			if err != nil {
				return fmt.Errorf("failed to cordon nodes: %v", err)
			}
		}

		logger.Infof("cordoned nodes: %s", strings.Join(cordonedNodes, ","))
		logger.Infof("terminated pods: %s", strings.Join(pods, ","))

		if err = deletePods(ctx, c.kclient, c.namespace, pods); err != nil {
			return err
		}

		for _, cl := range claims {
			for {
				p, _, e := tools.PVCPodUsage(ctx, c.kclient, c.namespace, cl.name)
				if e != nil {
					return fmt.Errorf("failed to find pods using pvc: %v", e)
				}

				if len(p) == 0 {
					break
				}

				logger.Infof("waiting pods: %s", strings.Join(p, " "))

				time.Sleep(2 * time.Second)
			}
		}
	}

	if err = commitWhenReleased(ctx, held, targets...); err != nil {
		return fmt.Errorf("failed to swap persistentvolumeclaims: %v", err)
	}

	done = true

	logger.Infof("persistentvolumeclaims %s,%s has been swapped", src.name, dst.name)

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
		{Group: "", Namespace: "", Resource: "nodes", Verb: "patch"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
