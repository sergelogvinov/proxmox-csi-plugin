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
	"time"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// WaitForCSIStorageCapacity polls until a CSIStorageCapacity object exists
// in namespace (the namespace the CSI controller/external-provisioner runs
// in - CSIStorageCapacity is itself a namespaced resource) whose
// storageClassName matches storageClassName, and returns it.
//
// The object showing up at all is what's under test here: per the
// CSIStorageCapacity API doc, "no object exists with suitable ... storage
// class name" is one of the ways a driver can report "no capacity
// available", so a missing object is meaningfully different from one that
// exists with an unset/zero capacity - this only asserts the former.
func WaitForCSIStorageCapacity(ctx context.Context, clientset *kubernetes.Clientset, namespace, storageClassName string, timeout time.Duration) (*storagev1.CSIStorageCapacity, error) {
	var found *storagev1.CSIStorageCapacity

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		list, err := clientset.StorageV1().CSIStorageCapacities(namespace).List(ctx, metav1.ListOptions{})

		switch {
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		for i := range list.Items {
			if list.Items[i].StorageClassName == storageClassName {
				found = &list.Items[i]

				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("no CSIStorageCapacity found for storageclass %s in namespace %s: %w", storageClassName, namespace, err)
	}

	return found, nil
}
