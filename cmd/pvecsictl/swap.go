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

	rbacv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	flags.BoolP("force", "f", false, "force migration even if the PersistentVolumeClaims is in use")
}

// nolint: cyclop, gocyclo
func (c *swapCmd) runSwap(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	var err error

	ctx := context.Background()

	srcPVC, srcPV, err := tools.PVCResources(ctx, c.kclient, c.namespace, args[0])
	if err != nil {
		return fmt.Errorf("failed to get resources: %v", err)
	}

	srcPods, srcVMName, err := tools.PVCPodUsage(ctx, c.kclient, c.namespace, args[0])
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	dstPVC, dstPV, err := tools.PVCResources(ctx, c.kclient, c.namespace, args[1])
	if err != nil {
		return fmt.Errorf("failed to get resources: %v", err)
	}

	dstPods, dstVMName, err := tools.PVCPodUsage(ctx, c.kclient, c.namespace, args[1])
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	cordonedNodes := []string{}

	defer func() {
		if len(cordonedNodes) > 0 {
			logger.Infof("uncordoning nodes: %s", strings.Join(cordonedNodes, ","))

			if err = tools.UncondonNodes(ctx, c.kclient, cordonedNodes); err != nil {
				logger.Errorf("failed to uncordon nodes: %v", err)
			}
		}
	}()

	if len(srcPods) > 0 || len(dstPods) > 0 {
		if force {
			var csiNodes []string

			if srcPV.Spec.CSI == nil || dstPV.Spec.CSI == nil {
				return fmt.Errorf("only CSI PersistentVolumes can be swapped in force mode")
			}

			if len(srcPods) > 0 {
				logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force swap\n", strings.Join(srcPods, ","), srcVMName)

				csiNodes, err = cordoneNodeWithPVs(ctx, c.kclient, srcPV)
				if err != nil {
					return fmt.Errorf("failed to cordon nodes: %v", err)
				}

				cordonedNodes = append(cordonedNodes, csiNodes...)
			}

			if len(dstPods) > 0 {
				logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force swap\n", strings.Join(dstPods, ","), dstVMName)

				csiNodes, err = cordoneNodeWithPVs(ctx, c.kclient, dstPV)
				if err != nil {
					return fmt.Errorf("failed to cordon nodes: %v", err)
				}

				cordonedNodes = append(cordonedNodes, csiNodes...)
			}

			logger.Infof("cordoned nodes: %s", strings.Join(cordonedNodes, ","))

			pods := srcPods
			pods = append(pods, dstPods...)

			logger.Infof("terminated pods: %s", strings.Join(pods, ","))

			for _, pod := range pods {
				if err = c.kclient.CoreV1().Pods(c.namespace).Delete(ctx, pod, metav1.DeleteOptions{}); err != nil {
					return fmt.Errorf("failed to delete pod: %v", err)
				}
			}

			waitPods := func(pod string) error {
				for {
					p, _, e := tools.PVCPodUsage(ctx, c.kclient, c.namespace, pod)
					if e != nil {
						return fmt.Errorf("failed to find pods using pvc: %v", e)
					}

					if len(p) == 0 {
						break
					}

					logger.Infof("waiting pods: %s", strings.Join(p, " "))

					time.Sleep(2 * time.Second)
				}

				return nil
			}

			if err := waitPods(args[0]); err != nil {
				return err
			}

			if err := waitPods(args[1]); err != nil {
				return err
			}

			time.Sleep(5 * time.Second)
		} else {
			if len(srcPods) > 0 {
				return fmt.Errorf("persistentvolumeclaims is using by pods: %s on the node %s, cannot swap pvc", strings.Join(srcPods, ","), srcVMName)
			}

			if len(dstPods) > 0 {
				return fmt.Errorf("persistentvolumeclaims is using by pods: %s on the node %s, cannot swap pvc", strings.Join(dstPods, ","), dstVMName)
			}
		}
	}

	err = swapPVC(ctx, c.kclient, c.namespace, srcPVC, srcPV, dstPVC, dstPV)
	if err != nil {
		cordonedNodes = []string{}

		return fmt.Errorf("failed to swap persistentvolumeclaims: %v", err)
	}

	logger.Infof("persistentvolumeclaims %s,%s has been swapped", args[0], args[1])

	return nil
}

// nolint: dupl
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
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "create"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "pods", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "nodes", Verb: "patch"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
