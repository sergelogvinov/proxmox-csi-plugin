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
	"bytes"
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecInPod runs command inside container of the named pod and returns its
// stdout/stderr. Used to verify what's actually on disk (mounted filesystem
// size, file checksums, ...) rather than trusting Kubernetes object status
// alone.
//
// stdin may be nil when the command needs none. Pass one to feed a command
// like `cryptsetup ... --key-file=-` a secret without ever putting it on the
// command line (where it would leak into process listings/audit logs).
func ExecInPod(ctx context.Context, restConfig *rest.Config, clientset *kubernetes.Clientset, namespace, podName, container string,
	command []string, stdin io.Reader,
) (stdout, stderr string, err error) {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   command,
		Stdin:     stdin != nil,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("failed to build exec executor: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})
	if err != nil {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("exec %v in pod %s/%s failed: %w (stderr: %s)", command, namespace, podName, err, stderrBuf.String())
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}
