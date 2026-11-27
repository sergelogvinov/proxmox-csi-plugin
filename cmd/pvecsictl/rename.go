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

	flags.BoolP("force", "f", false, "terminate the pods using the PersistentVolumeClaims")
	flags.Int("timeout", defaultRebindTimeout, "timeout in seconds")
}

func (c *renameCmd) runRename(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force")    //nolint: errcheck
	timeout, _ := flags.GetInt("timeout") //nolint: errcheck

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(timeout))
	defer cancel()

	src, dst := args[0], args[1]

	if src == dst {
		return fmt.Errorf("source and destination persistentvolumeclaims names must be different")
	}

	if errs := validation.IsDNS1123Subdomain(dst); len(errs) > 0 {
		return fmt.Errorf("invalid destination persistentvolumeclaims name %q: %s", dst, strings.Join(errs, ", "))
	}

	if _, err := c.kclient.CoreV1().PersistentVolumeClaims(c.namespace).Get(ctx, dst, metav1.GetOptions{}); err == nil {
		return fmt.Errorf("destination persistentvolumeclaims %s already exists", dst)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check destination persistentvolumeclaims %s: %v", dst, err)
	}

	if err := rebindClaims(ctx, c.kclient, c.namespace, force, []string{src}, rebind.Target{Claim: src, NewName: dst}); err != nil {
		return fmt.Errorf("failed to rename persistentvolumeclaims: %v", err)
	}

	logger.Infof("persistentvolumeclaims %s has been renamed to %s", src, dst)

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
