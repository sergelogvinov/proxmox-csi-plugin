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

	cobra "github.com/spf13/cobra"

	goproxmox "github.com/sergelogvinov/go-proxmox"
	csiconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	pxpool "github.com/sergelogvinov/proxmox-csi-plugin/pkg/proxmoxpool"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	rbacv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

type cleanCmd struct {
	pclient *pxpool.ProxmoxPool
	kclient *clientkubernetes.Clientset
}

func buildCleanCmd() *cobra.Command {
	c := &cleanCmd{}

	cmd := cobra.Command{
		Use:           "clean storage-ID proxmox-node",
		Aliases:       []string{"c"},
		Short:         "Clean volumes on Proxmox node with specified storage-ID",
		Args:          cobra.ExactArgs(2),
		PreRunE:       c.cleanValidate,
		RunE:          c.runClean,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	setCleanCmdFlags(&cmd)

	return &cmd
}

func setCleanCmdFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.BoolP("force", "f", false, "force delete volumes")
}

// nolint: cyclop, gocyclo
func (c *cleanCmd) runClean(cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	ctx := context.Background()
	storageID := args[0]
	node := args[1]
	nodeFound := false

	for _, region := range c.pclient.GetRegions() {
		cl, err := c.pclient.GetProxmoxCluster(region)
		if err != nil {
			return fmt.Errorf("failed to get Proxmox cluster client for region %s: %v", region, err)
		}

		if _, err := cl.GetNodeByName(ctx, node); err != nil {
			if errors.Is(err, goproxmox.ErrNodeNotFound) {
				continue
			}

			return fmt.Errorf("failed to get node %s on region %s: %v", node, region, err)
		}

		nodeFound = true

		_, err = cl.GetClusterStorage(ctx, storageID)
		if err != nil {
			return fmt.Errorf("failed to get cluster storage %s on region %s: %v", storageID, region, err)
		}

		pvs, err := cl.GetStorageContent(ctx, node, storageID)
		if err != nil {
			return fmt.Errorf("failed to get storage content for storage %s on region %s: %v", storageID, region, err)
		}

		if len(pvs) == 0 {
			return fmt.Errorf("no volumes found on storage %s on region %s", storageID, region)
		}

		k8sPVs, err := c.kclient.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list Kubernetes PersistentVolumes: %v", err)
		}

		for _, proxmoxPV := range pvs {
			volName, ok := strings.CutPrefix(proxmoxPV.Volid, storageID+":")
			if !ok {
				continue
			}

			vol := volume.NewVolume(region, node, storageID, volName)
			if vol.VMID() != "9999" {
				continue
			}

			found := false

			for _, k8sPV := range k8sPVs.Items {
				if k8sPV.Spec.CSI != nil && k8sPV.Spec.CSI.Driver == csi.DriverName && k8sPV.Spec.CSI.VolumeHandle == vol.VolumeID() {
					found = true

					break
				}
			}

			if !found {
				if force {
					fmt.Printf("Delete volume %s with size %dGi on storage %s on region %s\n", vol.Disk(), proxmoxPV.Size/1024/1024/1024, storageID, region)

					if err := cl.DeleteVMDisk(ctx, node, storageID, vol.Disk()); err != nil {
						return fmt.Errorf("failed to delete volume %s on storage %s on region %s: %v", volName, storageID, region, err)
					}
				} else {
					fmt.Printf("Found unused volume %s with size %dGi on storage %s on region %s\n", vol.Disk(), proxmoxPV.Size/1024/1024/1024, storageID, region)
				}
			}
		}
	}

	if !nodeFound {
		return fmt.Errorf("node %s not found", node)
	}

	return nil
}

// nolint: dupl,goconst
func (c *cleanCmd) cleanValidate(_ *cobra.Command, _ []string) error {
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

	kclientConfig, _, err := tools.BuildConfig(kubeconfig, "kube-default")
	if err != nil {
		return fmt.Errorf("failed to create kubernetes config: %v", err)
	}

	c.kclient, err = clientkubernetes.NewForConfig(kclientConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	accessCheck := []rbacv1.ResourceAttributes{
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "get"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "list"},
		{Group: "", Namespace: "", Resource: "nodes", Verb: "get"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
