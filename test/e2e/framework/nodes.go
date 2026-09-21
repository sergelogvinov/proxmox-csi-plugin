//go:build e2e

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

package framework

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
)

// ZonesInSameRegion returns the distinct TopologyZoneLabel values carried
// by Ready, schedulable nodes within the given corev1.LabelTopologyRegion,
// sorted for determinism.
func ZonesInSameRegion(ctx context.Context, clientset *kubernetes.Clientset, region string) ([]string, error) {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	seen := map[string]bool{}

	var zones []string

	for i := range nodes.Items {
		node := &nodes.Items[i]
		if node.Spec.Unschedulable || node.Labels[corev1.LabelTopologyRegion] != region {
			continue
		}

		zone := node.Labels[corev1.LabelTopologyZone]
		if zone == "" || seen[zone] {
			continue
		}

		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				seen[zone] = true

				zones = append(zones, zone)

				break
			}
		}
	}

	sort.Strings(zones)

	return zones, nil
}

// SelectTargetNode returns the node to pin a single-node-scoped test to:
// preferredName if set (E2E_NODE_NAME), otherwise the first Ready,
// schedulable node found.
func SelectTargetNode(ctx context.Context, clientset *kubernetes.Clientset, preferredName string) (*corev1.Node, error) {
	if preferredName != "" {
		node, err := clientset.CoreV1().Nodes().Get(ctx, preferredName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get node %q: %w", preferredName, err)
		}

		return node, nil
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	for i := range nodes.Items {
		node := &nodes.Items[i]
		if node.Spec.Unschedulable {
			continue
		}

		if hasBlockingTaint(node) {
			continue
		}

		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				return node, nil
			}
		}
	}

	return nil, fmt.Errorf("no Ready, schedulable node found (set E2E_NODE_NAME to pin one explicitly)")
}

// FindPodOnNode returns the first Running pod matching labelSelector in
// namespace that is scheduled onto nodeName. Used to locate the CSI
// node-plugin pod colocated with a workload pod under test.
func FindPodOnNode(ctx context.Context, clientset *kubernetes.Clientset, namespace, labelSelector, nodeName string) (*corev1.Pod, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", nodeName).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in %s matching %q on node %s: %w", namespace, labelSelector, nodeName, err)
	}

	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			return &pods.Items[i], nil
		}
	}

	return nil, fmt.Errorf("no running pod matching %q found in namespace %s on node %s", labelSelector, namespace, nodeName)
}

// hasBlockingTaint reports whether the node carries a NoSchedule taint that
// NewEphemeralPod does not tolerate.
func hasBlockingTaint(node *corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect != corev1.TaintEffectNoSchedule {
			continue
		}

		return true
	}

	return false
}
