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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

type renameCmd struct {
	kclient   *clientkubernetes.Clientset
	namespace string
}

func buildRenameCmd() *cobra.Command {
	c := &renameCmd{}

	cmd := cobra.Command{
		Use:           "rename pvc-old pvc-new",
		Aliases:       []string{"re"},
		Short:         "Rename PersistentVolumeClaim",
		Args:          cobra.ExactArgs(2),
		PreRunE:       c.renameValidate,
		RunE:          c.runRename,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	setrenameCmdFlags(&cmd)

	return &cmd
}

func setrenameCmdFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringP("namespace", "n", "", "namespace of the PersistentVolumeClaims")

	flags.BoolP("force", "f", false, "force rename even if the PersistentVolumeClaims is in use")
	flags.Int("timeout", rebindTimeout, "timeout in seconds")
}

// nolint: cyclop, gocyclo
func (c *renameCmd) runRename(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	seconds, _ := flags.GetInt("timeout") //nolint: errcheck

	timeout, err := commandTimeout(seconds, rebindTimeout)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	oldName, newName := args[0], args[1]

	if oldName == newName {
		return fmt.Errorf("source and destination persistentvolumeclaims names must be different")
	}

	if errs := validation.IsDNS1123Subdomain(newName); len(errs) > 0 {
		return fmt.Errorf("invalid destination persistentvolumeclaims name %q: %s", newName, strings.Join(errs, ", "))
	}

	if _, err := c.kclient.CoreV1().PersistentVolumeClaims(c.namespace).Get(ctx, newName, metav1.GetOptions{}); err == nil {
		return fmt.Errorf("destination persistentvolumeclaims %s already exists", newName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check destination persistentvolumeclaims %s: %v", newName, err)
	}

	srcPVC, srcPV, err := tools.PVCResources(ctx, c.kclient, c.namespace, oldName)
	if err != nil {
		return fmt.Errorf("failed to get resources: %v", err)
	}

	// Fail fast, before anything is changed. The authoritative check is vacate.
	usedBy, err := tools.PVCPods(ctx, c.kclient, c.namespace, oldName)
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	pods, nodes, statefulSets := podUsage(oldName, usedBy)

	if len(pods) > 0 && !force {
		return fmt.Errorf("persistentvolumeclaims is using by pods: %s on node %s, cannot rename it", strings.Join(pods, ","), strings.Join(nodes, ","))
	}

	target := rebind.Target{Claim: oldName, NewName: newName}

	logger.Infof("holding persistentvolumeclaims %s", oldName)

	held, err := rebind.Hold(ctx, c.kclient, c.namespace, oldName)
	if err != nil {
		return holdError(err, []string{oldName}, []string{srcPV.Name})
	}

	done := false

	defer func() {
		if done {
			return
		}

		// The claim is back under its name, unless a pod still uses it: restart the pods deleted for it.
		if abortAndReport(ctx, c.namespace, held, []heldClaimRef{{Name: oldName, UID: srcPVC.UID, Volume: srcPV.Name, NewName: newName}}, "rename") {
			triggerStatefulSets(ctx, c.kclient, c.namespace, statefulSets)
		}
	}()

	if len(pods) > 0 {
		logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force rename", strings.Join(pods, ","), strings.Join(nodes, ","))
	}

	seen, err := vacate(ctx, c.kclient, c.namespace, []*corev1.PersistentVolumeClaim{srcPVC}, force)

	// Merged before the error check: pods may have been deleted already.
	mergeOwners(statefulSets, seen.StatefulSets)

	if err != nil {
		return err
	}

	if err = held.Commit(ctx, target); err != nil {
		return fmt.Errorf("failed to rename persistentvolumeclaims: %v", err)
	}

	done = true

	// The StatefulSets are not triggered: their pods use the old name, which is gone.
	logger.Infof("persistentvolumeclaims %s has been renamed", oldName)

	return nil
}

// nolint: dupl,goconst
func (c *renameCmd) renameValidate(cmd *cobra.Command, _ []string) error {
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
