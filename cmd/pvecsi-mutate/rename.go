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

	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	flags.StringP("namespace", "n", "", "namespace of the persistentvolumeclaims")

	flags.BoolP("force", "f", false, "force migration even if the persistentvolumeclaims is in use")
	flags.Int("timeout", 120, "task timeout in seconds")
}

// nolint: cyclop, gocyclo
func (c *renameCmd) runRename(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	var err error

	ctx := context.Background()

	srcPVC, srcPV, err := tools.PVCResources(ctx, c.kclient, c.namespace, args[0])
	if err != nil {
		return fmt.Errorf("failed to get resources: %v", err)
	}

	pods, vmName, err := tools.PVCPodUsage(ctx, c.kclient, c.namespace, args[0])
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	cordonedNodes := []string{}

	if len(pods) > 0 {
		if force {
			logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force migration\n", strings.Join(pods, ","), vmName)

			var csiNodes []string

			csiNodes, err = tools.CSINodes(ctx, c.kclient, srcPV.Spec.CSI.Driver)
			if err != nil {
				return err
			}

			cordonedNodes = append(cordonedNodes, csiNodes...)

			logger.Infof("cordoning nodes: %s", strings.Join(cordonedNodes, ","))

			if _, err = tools.CondonNodes(ctx, c.kclient, cordonedNodes); err != nil {
				return fmt.Errorf("failed to cordon nodes: %v", err)
			}

			logger.Infof("terminated pods: %s", strings.Join(pods, ","))

			for _, pod := range pods {
				if err = c.kclient.CoreV1().Pods(c.namespace).Delete(ctx, pod, metav1.DeleteOptions{}); err != nil {
					return fmt.Errorf("failed to delete pod: %v", err)
				}
			}

			for {
				p, _, e := tools.PVCPodUsage(ctx, c.kclient, c.namespace, args[0])
				if e != nil {
					return fmt.Errorf("failed to find pods using pvc: %v", e)
				}

				if len(p) == 0 {
					break
				}

				logger.Infof("waiting pods: %s", strings.Join(p, " "))

				time.Sleep(2 * time.Second)
			}

			time.Sleep(5 * time.Second)
		} else {
			return fmt.Errorf("persistentvolumeclaims is using by pods: %s on node %s, cannot move volume", strings.Join(pods, ","), vmName)
		}
	}

	err = renamePVC(ctx, c.kclient, c.namespace, srcPVC, srcPV, args[1])
	if err != nil {
		return fmt.Errorf("failed to rename persistentvolumeclaims: %v", err)
	}

	if force {
		logger.Infof("uncordoning nodes: %s", strings.Join(cordonedNodes, ","))

		if err = tools.UncondonNodes(ctx, c.kclient, cordonedNodes); err != nil {
			return fmt.Errorf("failed to uncordon nodes: %v", err)
		}
	}

	logger.Infof("persistentvolumeclaims %s has been renamed", args[0])

	return nil
}

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

	return nil
}
