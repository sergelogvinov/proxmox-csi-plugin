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
	"log"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SweepLeftoverNamespaces best-effort deletes namespaces from a previous,
// killed run: anything named "<prefix>-<suffix>" and carrying the e2e
// managed-by label. It does not wait for deletion to complete - it just
// kicks it off so it doesn't collide with the run about to start.
func SweepLeftoverNamespaces(ctx context.Context, clientset *kubernetes.Clientset, prefix string) {
	list, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=proxmox-csi-plugin-e2e",
	})
	if err != nil {
		log.Printf("e2e: failed to list namespaces for leftover sweep: %v", err)

		return
	}

	for _, ns := range list.Items {
		if !strings.HasPrefix(ns.Name, prefix+"-") {
			continue
		}

		if err := clientset.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{}); err != nil {
			log.Printf("e2e: failed to sweep leftover namespace %q: %v", ns.Name, err)
		} else {
			log.Printf("e2e: swept leftover namespace %q from a previous run", ns.Name)
		}
	}
}
