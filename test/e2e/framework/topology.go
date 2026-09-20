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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"
)

// NodeMatchesVolumeNodeAffinity reports whether node satisfies na.
//
// It delegates entirely to nodeaffinity.NewNodeSelector/Match - the same
// evaluator kube-scheduler and the PV binder use (component-helpers'
// storage/volume.CheckNodeAffinity is a thin wrapper around exactly this) -
// rather than a hand-rolled per-term/per-requirement reimplementation that
// could drift from it. That gets the full NodeSelectorTerm semantics for
// free: OR across terms, AND within a term across both MatchExpressions
// (against labels) and MatchFields (against node fields), and a nil/empty
// term matching nothing.
//
// A nil na or nil na.Required means the PV carries no node constraint at
// all, which for a topology-aware CSI driver's dynamically provisioned
// volume is itself a bug worth catching, so both are reported as
// non-matching, preserving the existing boolean contract.
func NodeMatchesVolumeNodeAffinity(na *corev1.VolumeNodeAffinity, node *corev1.Node) bool {
	if na == nil || na.Required == nil {
		return false
	}

	selector, err := nodeaffinity.NewNodeSelector(na.Required)
	if err != nil {
		return false
	}

	return selector.Match(node)
}
