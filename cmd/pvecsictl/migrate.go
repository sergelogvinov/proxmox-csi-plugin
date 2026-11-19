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
	"fmt"
	"slices"
	"strings"
	"time"

	cobra "github.com/spf13/cobra"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	csiconfig "github.com/sergelogvinov/proxmox-csi-plugin/pkg/config"
	"github.com/sergelogvinov/proxmox-csi-plugin/pkg/csi"
	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"
	toolsproxmox "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/proxmox"
	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	rbacv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientkubernetes "k8s.io/client-go/kubernetes"
)

// triggerAttempts and triggerInterval bound how long triggerStatefulSets waits for the
// StatefulSet controller to recreate the pod of a migrated claim.
const (
	triggerAttempts = 5
	triggerInterval = 5 * time.Second
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
	statefulSets := []string{}

	for _, pod := range usedBy {
		pods = append(pods, pod.Name)
		vmName = pod.Spec.NodeName
		statefulSets = appendStatefulSetOwner(statefulSets, &pod)
	}

	if len(pods) > 0 && !force {
		return fmt.Errorf("persistentvolumeclaims is using by pods: %s on node %s, cannot move volume", strings.Join(pods, ","), vmName)
	}

	locked := true
	unsafeToRestore := false
	diskMoved := false

	defer func() {
		if !locked {
			return
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cleanupCancel()

		if !unsafeToRestore {
			logger.Infof("restoring persistentvolumeclaims %s on proxmox node %s", pvc, vol.Node())

			err := c.restorePVC(cleanupCtx, kubePVC, kubePV, vol)
			if err == nil {
				c.triggerStatefulSets(cleanupCtx, pvc, statefulSets)

				return
			}

			logger.Errorf("failed to restore persistentvolumeclaims: %v", err)
		}

		// The claim is recreated for wherever the disk is at the point of failure.
		target := vol.Node()
		if diskMoved {
			target = node
		}

		c.reportRecovery(cleanupCtx, kubePVC, kubePV, vol, target)
	}()

	logger.Infof("locking persistentvolumeclaims %s", pvc)

	if err = lockPVC(ctx, c.kclient, c.namespace, kubePVC, kubePV); err != nil {
		return fmt.Errorf("failed to lock persistentvolumeclaims: %v", err)
	}

	podNodes, podStatefulSets, err := c.waitPVCUnused(ctx, kubePVC, force)

	// Merged before the error check: pods may have been deleted already, and a restored
	// claim needs their StatefulSets triggered too.
	for _, name := range podStatefulSets {
		if !slices.Contains(statefulSets, name) {
			statefulSets = append(statefulSets, name)
		}
	}

	if err != nil {
		return err
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

	unsafeToRestore = true

	if err = toolsproxmox.MoveQemuDisk(ctx, cluster, vol, node, taskTimeout); err != nil {
		return fmt.Errorf("failed to move disk: %v", err)
	}

	diskMoved = true

	logger.Infof("replacing persistentvolume topology")

	if err = replacePVTopology(ctx, c.kclient, c.namespace, kubePVC, kubePV, vol, node); err != nil {
		return fmt.Errorf("failed to replace PV topology: %v", err)
	}

	locked = false

	logger.Infof("persistentvolumeclaims %s has been migrated to proxmox node %s", pvc, node)

	c.triggerStatefulSets(ctx, pvc, statefulSets)

	return nil
}

// triggerStatefulSets annotates the StatefulSets of the pods that used the migrated claim. A
// StatefulSet that failed to create its pod while the claim was locked backs off, and may wait
// minutes before it notices the new claim. An update of the object requeues it right away.
// Only the metadata is changed, which does not roll out its pods. The controller may handle the
// update before its cache has the recreated claim, and back off again, so the trigger is repeated
// with a new annotation value until a pod using the claim is created, for a bounded number of
// attempts. The claim is already in place, so a failure here only delays the pod start and is
// just logged. It runs with its own timeout, as the given context may be about to expire after a
// long disk move.
func (c *migrateCmd) triggerStatefulSets(ctx context.Context, pvcName string, statefulSets []string) {
	if len(statefulSets) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()

	for attempt := 1; attempt <= triggerAttempts; attempt++ {
		created, err := c.pvcPodCreated(ctx, pvcName)
		if err != nil {
			logger.Warnf("failed to check pods of persistentvolumeclaims %s: %v", pvcName, err)
		}

		if created {
			return
		}

		// Each attempt needs a new value, an unchanged object is not an update.
		patch := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:%q}}}`, annMigratedAt, time.Now().UTC().Format(time.RFC3339Nano))

		for _, name := range statefulSets {
			logger.Infof("triggering statefulset %s (attempt %d/%d)", name, attempt, triggerAttempts)

			if _, err = c.kclient.AppsV1().StatefulSets(c.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil && !errors.IsNotFound(err) {
				logger.Warnf("failed to trigger statefulset %s of persistentvolumeclaims %s: %v", name, pvcName, err)
			}
		}

		select {
		case <-ctx.Done():
			logger.Warnf("pod using persistentvolumeclaims %s was not created: %v", pvcName, ctx.Err())

			return
		case <-time.After(triggerInterval):
		}
	}

	created, err := c.pvcPodCreated(ctx, pvcName)
	if err != nil {
		logger.Warnf("failed to check pods of persistentvolumeclaims %s: %v", pvcName, err)
	}

	if !created {
		logger.Warnf("pod using persistentvolumeclaims %s was not created after %d triggers of statefulsets %s",
			pvcName, triggerAttempts, strings.Join(statefulSets, ","))
	}
}

// pvcPodCreated reports whether a pod that uses the claim exists and is not being deleted,
// whether it is scheduled yet or not.
func (c *migrateCmd) pvcPodCreated(ctx context.Context, pvcName string) (bool, error) {
	podList, err := c.kclient.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list pods: %v", err)
	}

	for _, pod := range podList.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}

		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				return true, nil
			}
		}
	}

	return false, nil
}

// appendStatefulSetOwner adds the StatefulSet controlling the pod, if any, to the list.
func appendStatefulSetOwner(statefulSets []string, pod *corev1.Pod) []string {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "StatefulSet" || slices.Contains(statefulSets, owner.Name) {
		return statefulSets
	}

	return append(statefulSets, owner.Name)
}

// waitPVCUnused waits until no pod can use the locked claim anymore. The kubelet refuses to
// start a pod with a Terminating claim only once pvcProtectionFinalizer is gone, and no
// finalizer can be added back to a Terminating object, so from then on the disk is safe to
// detach and move. Pods found using the claim meanwhile are deleted in force mode, and stop
// the migration otherwise. It returns the nodes and the StatefulSets of all pods seen using
// the claim, as the deleted pods cannot be looked up afterwards.
func (c *migrateCmd) waitPVCUnused(ctx context.Context, pvc *corev1.PersistentVolumeClaim, force bool) (nodes, statefulSets []string, err error) {
	deleted := map[string]bool{}

	for {
		var live *corev1.PersistentVolumeClaim

		live, err = c.kclient.CoreV1().PersistentVolumeClaims(c.namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
		if err != nil {
			return nodes, statefulSets, fmt.Errorf("failed to get pvc: %v", err)
		}

		if live.UID != pvc.UID {
			return nodes, statefulSets, fmt.Errorf("persistentvolumeclaims %s was replaced during the migration", pvc.Name)
		}

		var pods []corev1.Pod

		pods, err = tools.PVCPods(ctx, c.kclient, c.namespace, pvc.Name)
		if err != nil {
			return nodes, statefulSets, fmt.Errorf("failed to find pods using pvc: %v", err)
		}

		names := []string{}

		for _, pod := range pods {
			names = append(names, pod.Name)

			if !slices.Contains(nodes, pod.Spec.NodeName) {
				nodes = append(nodes, pod.Spec.NodeName)
			}

			statefulSets = appendStatefulSetOwner(statefulSets, &pod)
		}

		if len(pods) == 0 && !slices.Contains(live.Finalizers, pvcProtectionFinalizer) {
			return nodes, statefulSets, nil
		}

		if len(pods) > 0 && !force {
			return nodes, statefulSets, fmt.Errorf("persistentvolumeclaims %s started being used by pods: %s while it was being locked, re-run with --force to migrate it",
				pvc.Name, strings.Join(names, ","))
		}

		for _, pod := range pods {
			if deleted[pod.Name] {
				continue
			}

			logger.Infof("persistentvolumeclaims is using by pod %s on node %s, terminating it", pod.Name, pod.Spec.NodeName)

			if err = c.kclient.CoreV1().Pods(c.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
				return nodes, statefulSets, fmt.Errorf("failed to delete pod: %v", err)
			}

			deleted[pod.Name] = true
		}

		if len(pods) > 0 {
			logger.Infof("waiting pods: %s", strings.Join(names, " "))
		} else {
			logger.Infof("waiting for persistentvolumeclaims %s to be released", pvc.Name)
		}

		select {
		case <-ctx.Done():
			return nodes, statefulSets, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// restorePVC recreates the locked claim on its original Proxmox node. It is only safe before
// the disk move has started, and only once no pod holds the claim anymore: otherwise releasing
// migrateFinalizer would let the claim go away later with nothing left to recreate it.
func (c *migrateCmd) restorePVC(
	ctx context.Context,
	pvc *corev1.PersistentVolumeClaim,
	pv *corev1.PersistentVolume,
	vol *volume.Volume,
) error {
	live, err := c.kclient.CoreV1().PersistentVolumeClaims(c.namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get pvc: %v", err)
	}

	// Another claim took the name: leave it alone, and keep the PV retained, since the
	// original claim may already be gone and releasing its PV would delete the disk.
	if err == nil && live.UID != pvc.UID {
		return fmt.Errorf("persistentvolumeclaims %s was replaced, persistentvolume %s is left retained", pvc.Name, pv.Name)
	}

	// lockPVC failed before the claim was deleted: undo the lock in place.
	if err == nil && live.DeletionTimestamp == nil {
		if err = tools.PVCRemoveFinalizer(ctx, c.kclient, c.namespace, pvc.Name, pvc.UID, migrateFinalizer); err != nil {
			return err
		}

		return restoreReclaimPolicy(ctx, c.kclient, pv)
	}

	pods, _, err := tools.PVCPodUsage(ctx, c.kclient, c.namespace, pvc.Name)
	if err != nil {
		return fmt.Errorf("failed to find pods using pvc: %v", err)
	}

	if len(pods) > 0 {
		return fmt.Errorf("persistentvolumeclaims is still used by pods: %s", strings.Join(pods, ","))
	}

	return replacePVTopology(ctx, c.kclient, c.namespace, pvc, pv, vol, vol.Node())
}

// reportRecovery logs the state a failed migration left behind. While the claim is still held
// by migrateFinalizer, the original objects are intact. Once it is gone, the claim and possibly
// the PV have to be recreated, so their manifests for the node holding the disk are printed.
func (c *migrateCmd) reportRecovery(
	ctx context.Context,
	pvc *corev1.PersistentVolumeClaim,
	pv *corev1.PersistentVolume,
	vol *volume.Volume,
	node string,
) {
	live, err := c.kclient.CoreV1().PersistentVolumeClaims(c.namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
	if err == nil && live.UID == pvc.UID && slices.Contains(live.Finalizers, migrateFinalizer) {
		logger.Errorf("migration failed, leaving persistentvolumeclaims %s terminating with finalizer %s and persistentvolume %s retained for manual recovery",
			pvc.Name, migrateFinalizer, pv.Name)

		return
	}

	newPVC, newPV := newPVTopology(c.namespace, pvc, pv, vol, node)
	newPVC.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}
	newPV.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolume"}

	logger.Errorf("migration failed after persistentvolumeclaims %s was released, the disk %s on proxmox node %s is retained, "+
		"but persistentvolumeclaims %s and persistentvolume %s may be missing, recreate them from the manifests below",
		pvc.Name, vol.Disk(), node, pvc.Name, pv.Name)

	for _, obj := range []any{newPV, newPVC} {
		manifest, err := json.Marshal(obj)
		if err != nil {
			logger.Errorf("failed to marshal manifest: %v", err)

			continue
		}

		fmt.Println(string(manifest))
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
