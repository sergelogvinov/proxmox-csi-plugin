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
}

// nolint: cyclop, gocyclo
func (c *renameCmd) runRename(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	var err error

	ctx := context.Background()

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

	pods, vmName, err := tools.PVCPodUsage(ctx, c.kclient, c.namespace, oldName)
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	if len(pods) > 0 {
		if !force {
			return fmt.Errorf("persistentvolumeclaims is using by pods: %s on node %s, cannot rename it", strings.Join(pods, ","), vmName)
		}

		if srcPV.Spec.CSI == nil {
			return fmt.Errorf("only CSI PersistentVolumes can be renamed in force mode")
		}
	}

	cordonedNodes := []string{}
	unsafeToUncordon := false

	defer func() {
		if len(cordonedNodes) == 0 {
			return
		}

		if unsafeToUncordon {
			logger.Errorf("rename failed after storage changes started, leaving nodes cordoned for manual recovery: %s", strings.Join(cordonedNodes, ","))

			return
		}

		logger.Infof("uncordoning nodes: %s", strings.Join(cordonedNodes, ","))

		if err := tools.UncondonNodes(ctx, c.kclient, cordonedNodes); err != nil {
			logger.Errorf("failed to uncordon nodes: %v", err)
		}
	}()

	target := rebind.Target{Claim: oldName, NewName: newName}

	logger.Infof("holding persistentvolumeclaims %s", oldName)

	held, err := rebind.Hold(ctx, c.kclient, c.namespace, oldName)
	if err != nil {
		return holdError(err, []string{oldName}, []string{srcPV.Name})
	}

	done := false

	// Runs before the uncordon above, so it can tell whether the nodes are safe to uncordon.
	defer func() {
		if done {
			return
		}

		if !abortAndReport(ctx, c.namespace, held, []heldClaimRef{{Name: oldName, UID: srcPVC.UID, Volume: srcPV.Name, NewName: newName}}, "rename") {
			unsafeToUncordon = true
		}
	}()

	if len(pods) > 0 {
		logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force rename\n", strings.Join(pods, ","), vmName)

		cordonedNodes, err = cordoneNodeWithPVs(ctx, c.kclient, srcPV)
		if err != nil {
			return fmt.Errorf("failed to cordon nodes: %v", err)
		}

		logger.Infof("cordoned nodes: %s", strings.Join(cordonedNodes, ","))
		logger.Infof("terminated pods: %s", strings.Join(pods, ","))

		if err = deletePods(ctx, c.kclient, c.namespace, pods); err != nil {
			return err
		}

		for {
			p, _, e := tools.PVCPodUsage(ctx, c.kclient, c.namespace, oldName)
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

	if err = commitWhenReleased(ctx, held, target); err != nil {
		return fmt.Errorf("failed to rename persistentvolumeclaims: %v", err)
	}

	done = true

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
		{Group: "", Namespace: "", Resource: "nodes", Verb: "patch"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
