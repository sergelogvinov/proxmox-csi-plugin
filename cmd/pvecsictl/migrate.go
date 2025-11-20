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

	csiconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	toolsproxmox "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/proxmox"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	rbacv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

type migrateCmd struct {
	pclient   *pxpool.ProxmoxPool
	kclient   *clientkubernetes.Clientset
	namespace string
}

func buildMigrateCmd() *cobra.Command {
	c := &migrateCmd{}

	cmd := cobra.Command{
		Use:           "migrate pvc proxmox-node",
		Aliases:       []string{"m"},
		Short:         "Migrate data from one Proxmox node to another",
		Args:          cobra.ExactArgs(2),
		PreRunE:       c.migrationValidate,
		RunE:          c.runMigration,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	setMigrateCmdFlags(&cmd)

	return &cmd
}

func setMigrateCmdFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringP("namespace", "n", "", "namespace of the persistentvolumeclaims")

	flags.BoolP("force", "f", false, "force migration even if the persistentvolumeclaims is in use")
	flags.Int("timeout", 10800, "task timeout in seconds")
}

// nolint: cyclop, gocyclo
func (c *migrateCmd) runMigration(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	var err error

	ctx := context.Background()
	pvc := args[0]
	node := args[1]

	kubePVC, kubePV, err := tools.PVCResources(ctx, c.kclient, c.namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to get resources: %v", err)
	}

	vol, err := volume.NewVolumeFromVolumeID(kubePV.Spec.CSI.VolumeHandle)
	if err != nil {
		return fmt.Errorf("failed to parse volume ID: %v", err)
	}

	if vol.Node() == node {
		return fmt.Errorf("persistentvolumeclaims %s is already on proxmox node %s", pvc, node)
	}

	cluster, err := c.pclient.GetProxmoxCluster(vol.Cluster())
	if err != nil {
		return fmt.Errorf("failed to get Proxmox cluster: %v", err)
	}

	pods, vmName, err := tools.PVCPodUsage(ctx, c.kclient, c.namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	cordonedNodes := []string{}

	if len(pods) > 0 {
		if force {
			logger.Infof("persistentvolumeclaims is using by pods: %s on node %s, trying to force migration\n", strings.Join(pods, ","), vmName)

			var csiNodes []string

			csiNodes, err = tools.CSINodes(ctx, c.kclient, csi.DriverName)
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
				p, _, e := tools.PVCPodUsage(ctx, c.kclient, c.namespace, pvc)
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

	if err = toolsproxmox.WaitForVolumeDetach(ctx, cluster, vmName, vol.Disk()); err != nil {
		return fmt.Errorf("failed to wait for volume detach: %v", err)
	}

	logger.Infof("moving disk %s to proxmox node %s", vol.Disk(), node)

	taskTimeout, _ := flags.GetInt("timeout") //nolint: errcheck
	if err = toolsproxmox.MoveQemuDisk(ctx, cluster, vol, node, taskTimeout); err != nil {
		return fmt.Errorf("failed to move disk: %v", err)
	}

	logger.Infof("replacing persistentvolume topology")

	if err = replacePVTopology(ctx, c.kclient, c.namespace, kubePVC, kubePV, vol, node); err != nil {
		return fmt.Errorf("failed to replace PV topology: %v", err)
	}

	if force {
		logger.Infof("uncordoning nodes: %s", strings.Join(cordonedNodes, ","))

		if err = tools.UncondonNodes(ctx, c.kclient, cordonedNodes); err != nil {
			return fmt.Errorf("failed to uncordon nodes: %v", err)
		}
	}

	logger.Infof("persistentvolumeclaims %s has been migrated to proxmox node %s", pvc, node)

	return nil
}

// nolint: dupl
func (c *migrateCmd) migrationValidate(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()

	cfg, err := csiconfig.ReadCloudConfigFromFile(cloudconfig)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	for _, c := range cfg.Clusters {
		if c.Username == "" || c.Password == "" {
			return fmt.Errorf("this command requires Proxmox root account, please provide username and password in config file (cluster=%s)", c.Region)
		}
	}

	c.pclient, err = pxpool.NewProxmoxPool(cfg.Clusters)
	if err != nil {
		return fmt.Errorf("failed to create Proxmox cluster client: %v", err)
	}

	if err = c.pclient.CheckClusters(context.TODO()); err != nil {
		return fmt.Errorf("failed to initialize Proxmox clusters: %v", err)
	}

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
