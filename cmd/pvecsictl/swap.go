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

	cobra "github.com/spf13/cobra"

	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"

	rbacv1 "k8s.io/api/authorization/v1"
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

	flags.BoolP("force", "f", false, "terminate the pods using the PersistentVolumeClaims")
	flags.Int("timeout", defaultRebindTimeout, "timeout in seconds")
}

func (c *swapCmd) runSwap(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force")    //nolint: errcheck
	timeout, _ := flags.GetInt("timeout") //nolint: errcheck

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(timeout))
	defer cancel()

	a, b := args[0], args[1]

	if a == b {
		return fmt.Errorf("persistentvolumeclaims names must be different")
	}

	targets := []rebind.Target{{Claim: a, NewName: b}, {Claim: b, NewName: a}}

	if err := rebindClaims(ctx, c.kclient, c.namespace, force, []string{a, b}, targets...); err != nil {
		return fmt.Errorf("failed to swap persistentvolumeclaims: %v", err)
	}

	logger.Infof("persistentvolumeclaims %s,%s has been swapped", a, b)

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
		{Group: "", Namespace: "", Resource: "persistentvolumeclaims", Verb: "create"},
		{Group: "", Namespace: "", Resource: "persistentvolumeclaims", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "persistentvolumeclaims", Verb: "update"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "create"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "patch"},
		{Group: "", Namespace: "", Resource: "pods", Verb: "delete"},
		{Group: "apps", Namespace: "", Resource: "statefulsets", Verb: "patch"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
