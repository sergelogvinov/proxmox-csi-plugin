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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	cobra "github.com/spf13/cobra"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	csiconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes/rebind"
	toolsproxmox "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/proxmox"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	rbacv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

type migrateCmd struct {
	pclient   *pxpool.ProxmoxPool
	kclient   clientkubernetes.Interface
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
func (c *migrateCmd) runMigration(cmd *cobra.Command, args []string) (retErr error) {
	flags := cmd.Flags()
	force, _ := flags.GetBool("force") //nolint: errcheck

	var err error

	taskTimeout, _ := flags.GetInt("timeout") //nolint: errcheck
	if taskTimeout <= 0 {
		taskTimeout = 10800
	}

	// Bound the whole command, not just the disk-copy task: pod eviction and
	// volume-detach waits below could otherwise block forever on a background
	// context with no deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(taskTimeout)*time.Second)
	defer cancel()

	pvc := args[0]
	node := args[1]

	kubePVC, kubePV, err := tools.PVCResources(ctx, c.kclient, c.namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to get resources: %v", err)
	}

	if kubePV.Spec.CSI == nil || kubePV.Spec.CSI.Driver != csi.DriverName {
		return fmt.Errorf("persistentvolume %s is not provisioned by Proxmox CSI driver", kubePV.Name)
	}

	vol, err := volume.NewVolumeFromVolumeID(kubePV.Spec.CSI.VolumeHandle)
	if err != nil {
		return fmt.Errorf("failed to parse volume ID: %v", err)
	}

	if vol.Node() == node {
		return fmt.Errorf("persistentvolumeclaims %s is already on proxmox node %s", pvc, node)
	}

	cluster, err := c.pclient.Get(vol.Cluster())
	if err != nil {
		return fmt.Errorf("failed to get Proxmox cluster: %v", err)
	}

	// Fail fast, before anything is changed. The authoritative check follows the lock.
	usedBy, err := tools.PVCPods(ctx, c.kclient, c.namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	pods := []string{}
	vmName := ""

	for _, pod := range usedBy {
		pods = append(pods, pod.Name)
		vmName = pod.Spec.NodeName
	}

	if len(pods) > 0 && !force {
		return fmt.Errorf("persistentvolumeclaims is using by pods: %s on node %s, cannot move volume", strings.Join(pods, ","), vmName)
	}

	// The target the claim is recreated for, once the disk is on the new node.
	target := rebind.Target{
		Claim:        pvc,
		VolumeHandle: volume.NewVolume(vol.Region(), node, vol.Storage(), vol.Disk()).VolumeID(),
		NodeAffinity: nodeAffinity(vol.Region(), node),
	}

	logger.Infof("holding persistentvolumeclaims %s", pvc)

	held, err := rebind.Hold(ctx, c.kclient, c.namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to hold persistentvolumeclaims: %v", err)
	}

	held.Logf = logger.Infof

	done := false
	moveStarted := false
	diskMoved := false

	defer func() {
		if done {
			return
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cleanupCancel()

		switch {
		case !moveStarted:
			logger.Infof("restoring persistentvolumeclaims %s on proxmox node %s", pvc, vol.Node())

			err := held.Abort(cleanupCtx)
			if err != nil && !errors.Is(err, rebind.ErrInUse) {
				logger.Warnf("failed to restore persistentvolumeclaims, retrying: %v", err)

				err = held.Abort(cleanupCtx)
			}

			if err == nil {
				triggerStatefulSets(cleanupCtx, held)

				return
			}

			logger.Errorf("failed to restore persistentvolumeclaims: %v", err)

			c.reportRecovery(cleanupCtx, held, rebind.Target{Claim: pvc}, kubePVC, kubePV, recoveryBeforeMove, vol.Node(), node)

		case diskMoved:
			logger.Infof("retrying to recreate persistentvolumeclaims %s on proxmox node %s", pvc, node)

			err := held.Commit(cleanupCtx, target)
			if err == nil {
				logger.Infof("persistentvolumeclaims %s has been migrated to proxmox node %s", pvc, node)
				triggerStatefulSets(cleanupCtx, held)

				retErr = nil

				return
			}

			logger.Errorf("failed to recreate persistentvolumeclaims: %v", err)

			c.reportRecovery(cleanupCtx, held, target, kubePVC, kubePV, recoveryAfterMove, vol.Node(), node)

		default:
			c.reportRecovery(cleanupCtx, held, rebind.Target{Claim: pvc}, kubePVC, kubePV, recoveryMoveFailed, vol.Node(), node)
		}
	}()

	podNodes, err := held.Drain(ctx, force)
	if err != nil {
		if errors.Is(err, rebind.ErrInUse) {
			return fmt.Errorf("persistentvolumeclaims %s started being used while it was being held, re-run with --force to migrate it: %v", pvc, err)
		}

		return fmt.Errorf("failed to wait for persistentvolumeclaims %s to be released: %v", pvc, err)
	}

	// The disk has to be detached from every node a pod was seen on, including one from the
	// first check whose pod was gone by the time of a later one.
	if vmName != "" && !slices.Contains(podNodes, vmName) {
		podNodes = append(podNodes, vmName)
	}

	for _, podNode := range podNodes {
		// Uses providerID first, then falls back to the proxmox.sinextra.dev/instance-id annotation.
		kubeNode, nodeErr := c.kclient.CoreV1().Nodes().Get(ctx, podNode, metav1.GetOptions{})
		if nodeErr != nil {
			return fmt.Errorf("failed to get kubernetes node %s: %v", podNode, nodeErr)
		}

		vmID, nodeErr := csi.ProxmoxVMIDbyNode(kubeNode)
		if nodeErr != nil {
			return fmt.Errorf("failed to resolve Proxmox VMID from node %s: %v", podNode, nodeErr)
		}

		logger.Infof("waiting for disk %s to detach from kubernetes node %s (Proxmox VMID %d)", vol.Disk(), podNode, vmID)

		if err = toolsproxmox.WaitForVolumeDetach(ctx, cluster, vmID, vol.Disk()); err != nil {
			return fmt.Errorf("failed to wait for volume detach: %v", err)
		}
	}

	logger.Infof("moving disk %s to proxmox node %s", vol.Disk(), node)

	moveStarted = true

	if err = toolsproxmox.MoveQemuDisk(ctx, cluster, vol, node, taskTimeout); err != nil {
		return fmt.Errorf("failed to move disk: %v", err)
	}

	diskMoved = true

	logger.Infof("replacing persistentvolume topology")

	if err = held.Commit(ctx, target); err != nil {
		return fmt.Errorf("failed to replace PV topology: %v", err)
	}

	done = true

	logger.Infof("persistentvolumeclaims %s has been migrated to proxmox node %s", pvc, node)

	triggerStatefulSets(ctx, held)

	return nil
}

// recoveryStage is how far a failed migration got.
type recoveryStage int

const (
	// recoveryBeforeMove: the claim could not be restored, the disk is still on its node.
	recoveryBeforeMove recoveryStage = iota
	// recoveryMoveFailed: the disk move failed, the disk may be on either node.
	recoveryMoveFailed
	// recoveryAfterMove: the disk is on the new node, the claim could not be recreated there.
	recoveryAfterMove
)

// reportRecovery logs the state a failed migration left behind, and how to recover from it.
// pvc and pv are the objects the migration held.
func (c *migrateCmd) reportRecovery(
	ctx context.Context,
	held *rebind.Rebind,
	target rebind.Target,
	pvc *corev1.PersistentVolumeClaim,
	pv *corev1.PersistentVolume,
	stage recoveryStage,
	source, destination string,
) {
	// The cleanup context may be used up by the failed restore: look the claim up on a
	// context of its own.
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()

	live, err := c.kclient.CoreV1().PersistentVolumeClaims(c.namespace).Get(lookupCtx, target.Claim, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf("migration failed, and the state of persistentvolumeclaims %s is unknown: %v. "+
			"Check whether it is still terminating with its hold finalizer before changing anything, persistentvolume %s is retained",
			target.Claim, err, pv.Name)

		if stage == recoveryMoveFailed {
			logger.Errorf("the disk move failed, the disk may be on proxmox node %s or %s: check where it is before recovering it", source, destination)
		}

		// After a moved disk, the objects for the new node are needed whatever the claim's state.
		if stage != recoveryAfterMove {
			return
		}
	}

	stillHeld := err == nil && live.DeletionTimestamp != nil && rebind.IsHeld(live)

	// A claim under the name that the migration did not create must never get the disk.
	foreign := fmt.Sprintf("If persistentvolumeclaims %s exists without the annotation %s=%s, another claim took the name: stop, "+
		"and do not reserve the disk for it.", target.Claim, rebind.AnnRebindFrom, pvc.UID)

	switch stage {
	case recoveryBeforeMove, recoveryMoveFailed:
		if stillHeld {
			if stage == recoveryBeforeMove {
				logger.Errorf("migration failed, persistentvolumeclaims %s is left terminating with its hold finalizer and persistentvolume %s is retained, "+
					"the disk is on proxmox node %s, run the migration again to resume it", target.Claim, pv.Name, source)
			} else {
				logger.Errorf("disk move failed, persistentvolumeclaims %s is left terminating with its hold finalizer and persistentvolume %s is retained. "+
					"The disk may be on proxmox node %s or %s: check where it is before running the migration again or recovering it manually",
					target.Claim, pv.Name, source, destination)
			}

			return
		}

		if stage == recoveryMoveFailed {
			logger.Errorf("disk move failed after persistentvolumeclaims %s was released. The disk may be on proxmox node %s or %s: "+
				"check where it is first, the steps below are for proxmox node %s", target.Claim, source, destination, source)
		}

		logger.Errorf("persistentvolumeclaims %s was released, persistentvolume %s is retained. %s "+
			"Otherwise create the claim from the manifest below, then reserve the persistentvolume for it, with CLAIM_UID set to the uid of that claim:\n"+
			"  kubectl patch pv %s --type=json -p '[{\"op\":\"test\",\"path\":\"/metadata/uid\",\"value\":%q},"+
			"{\"op\":\"add\",\"path\":\"/spec/claimRef\",\"value\":{\"kind\":\"PersistentVolumeClaim\",\"apiVersion\":\"v1\",\"namespace\":%q,\"name\":%q,\"uid\":\"CLAIM_UID\"}}]'\n"+
			"Once the claim is Bound, restore the reclaim policy recorded in the %s annotation of the persistentvolume",
			target.Claim, pv.Name, foreign, pv.Name, pv.UID, c.namespace, target.Claim, rebind.AnnReclaimPolicy)

	case recoveryAfterMove:
		logger.Errorf("the disk was moved to proxmox node %s, but persistentvolumeclaims %s could not be recreated there. "+
			"Do not run the migration again, recreate the objects from the manifests below:", destination, target.Claim)

		if stillHeld {
			logger.Errorf("  1. persistentvolumeclaims %s is terminating with its hold finalizer, remove the finalizer so it is deleted", target.Claim)
		}

		logger.Errorf("  2. if persistentvolume %s still exists with the volume handle %s, delete it: it is retained, so the disk is kept", pv.Name, pv.Spec.CSI.VolumeHandle)
		logger.Errorf("  3. %s Otherwise create the claim from the first manifest, unless it exists with that annotation", foreign)
		logger.Errorf("  4. if persistentvolume %s exists with the new handle and the annotation %s=%s, and is not being deleted, reserve it for that claim, "+
			"with PV_UID set to its uid and CLAIM_UID to the uid of the claim:\n"+
			"  kubectl patch pv %s --type=json -p '[{\"op\":\"test\",\"path\":\"/metadata/uid\",\"value\":\"PV_UID\"},"+
			"{\"op\":\"add\",\"path\":\"/spec/claimRef\",\"value\":{\"kind\":\"PersistentVolumeClaim\",\"apiVersion\":\"v1\",\"namespace\":%q,\"name\":%q,\"uid\":\"CLAIM_UID\"}}]'\n"+
			"  Otherwise set spec.claimRef.uid in the persistentvolume manifest to the uid of that claim, then create the persistentvolume",
			pv.Name, rebind.AnnRebindFrom, pv.UID, pv.Name, c.namespace, target.Claim)
		logger.Errorf("  5. once the claim is Bound, restore the reclaim policy recorded in the %s annotation of the persistentvolume", rebind.AnnReclaimPolicy)
	}

	objs, err := held.Manifests(target)
	if err != nil {
		logger.Errorf("failed to build recovery manifests: %v", err)

		return
	}

	for _, obj := range objs {
		manifest, err := json.Marshal(obj)
		if err != nil {
			logger.Errorf("failed to marshal manifest: %v", err)

			continue
		}

		fmt.Println(string(manifest))
	}
}

// nodeAffinity returns the node affinity of a volume on the given Proxmox node.
func nodeAffinity(region, node string) *corev1.VolumeNodeAffinity {
	return &corev1.VolumeNodeAffinity{
		Required: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      corev1.LabelTopologyRegion,
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{region},
						},
						{
							Key:      corev1.LabelTopologyZone,
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{node},
						},
					},
				},
			},
		},
	}
}

// nolint: dupl,goconst
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

	if err = checkClusters(context.TODO(), c.pclient); err != nil {
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
		{Group: "", Namespace: "", Resource: "persistentvolumeclaims", Verb: "update"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "create"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "persistentvolumes", Verb: "patch"},
		{Group: "", Namespace: "", Resource: "pods", Verb: "delete"},
		{Group: "", Namespace: "", Resource: "nodes", Verb: "get"},
		{Group: "apps", Namespace: "", Resource: "statefulsets", Verb: "patch"},
	}

	return checkPermissions(context.TODO(), c.kclient, accessCheck)
}
