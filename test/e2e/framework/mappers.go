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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ListEncryptedMappers execs into a privileged pod (normally the CSI
// node-plugin pod, which has host device access) and returns the names of
// every dm-crypt mapper the driver created (i.e. everything under
// /dev/mapper ending in "-encrypted").
func ListEncryptedMappers(ctx context.Context, restConfig *rest.Config, clientset *kubernetes.Clientset, namespace, podName, container string) ([]string, error) {
	var mappers []string

	stdout, _, err := ExecInPod(ctx, restConfig, clientset, namespace, podName, container,
		[]string{"blkid", "--match-token", "TYPE=crypto_LUKS"}, nil)
	if err != nil {
		if strings.Contains(err.Error(), "exit code 2") {
			return mappers, nil
		}

		return nil, fmt.Errorf("failed to list encrypted mappers on pod %s/%s: %w", namespace, podName, err)
	}

	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "crypto_LUKS") {
			devRaw, _, _ := strings.Cut(line, ":")

			mappers = append(mappers, devRaw)
		}
	}

	return mappers, nil
}

// WaitForNewEncryptedMapper polls ListEncryptedMappers until a mapper shows
// up that wasn't present in before, and returns it. Used to identify which
// dm-crypt device a just-provisioned encrypted volume created, without
// needing to know the raw device name (SCSI slot) ahead of time.
func WaitForNewEncryptedMapper(ctx context.Context, restConfig *rest.Config, clientset *kubernetes.Clientset, namespace, podName, container string,
	before []string, timeout time.Duration,
) (string, error) {
	existing := make(map[string]bool, len(before))
	for _, m := range before {
		existing[m] = true
	}

	var found string

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		after, err := ListEncryptedMappers(ctx, restConfig, clientset, namespace, podName, container)

		switch {
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		for _, m := range after {
			if !existing[m] {
				found = m

				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("no new dm-crypt mapper appeared on pod %s/%s: %w", namespace, podName, err)
	}

	return found, nil
}
