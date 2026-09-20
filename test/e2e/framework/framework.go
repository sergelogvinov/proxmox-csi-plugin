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
	"math/rand/v2"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SharedClient and SharedConfig are set once by TestMain before any test
// runs, and read (never mutated) by every Framework created afterwards.
var (
	SharedClient *Client
	SharedConfig Config
)

// Framework is the per-test handle: it owns a disposable namespace on the
// shared Client/Config set up once in TestMain, and registers cleanup of
// everything it creates.
type Framework struct {
	T         *testing.T
	Client    *Client
	Config    Config
	Namespace string
}

// New creates a Framework for t: a fresh namespace on the shared cluster
// client, torn down via t.Cleanup regardless of test outcome.
func New(t *testing.T) *Framework {
	t.Helper()

	f := &Framework{
		T:      t,
		Client: SharedClient,
		Config: SharedConfig,
	}

	f.Namespace = fmt.Sprintf("%s-%s", f.Config.NamespacePrefix, randSuffix())

	f.Logf("creating namespace %s", f.Namespace)

	ctx, cancel := context.WithTimeout(context.Background(), f.Config.Timeout)
	defer cancel()

	ns := NewNamespace(f.Namespace)

	if _, err := f.Client.Clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create namespace %q: %v", f.Namespace, err)
	}

	t.Cleanup(func() {
		f.Logf("deleting namespace %s", f.Namespace)

		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), f.Config.Timeout)
		defer deleteCancel()

		if err := f.Client.Clientset.CoreV1().Namespaces().Delete(deleteCtx, f.Namespace, metav1.DeleteOptions{}); err != nil {
			f.Logf("failed to delete namespace %s: %v", f.Namespace, err)

			return
		}

		if err := WaitForNamespaceGone(deleteCtx, f.Client.Clientset, f.Namespace, f.Config.Timeout); err != nil {
			f.Logf("namespace %s did not clean up: %v", f.Namespace, err)

			return
		}

		f.Logf("namespace %s deleted", f.Namespace)
	})

	return f
}

// Context returns a context bound to the Framework's configured timeout.
func (f *Framework) Context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), f.Config.Timeout)
}

// Logf narrates what the test is doing right now: prefixed with a
// wall-clock timestamp and the test's namespace so a `go test -v` stream
// shows live progress through a scenario, not just the pass/fail result at
// the end.
func (f *Framework) Logf(format string, args ...any) {
	f.T.Helper()
	f.T.Logf("[%s] [%s] %s", time.Now().Format(time.TimeOnly), f.Namespace, fmt.Sprintf(format, args...))
}

// Step narrates the start of a named action and returns a function to call
// once it completes, which logs how long it took. Usage:
//
//	defer f.Step("waiting for pod %s to be ready", podName)()
func (f *Framework) Step(format string, args ...any) func() {
	f.T.Helper()

	msg := fmt.Sprintf(format, args...)
	f.Logf("-> %s", msg)

	start := time.Now()

	return func() {
		f.T.Helper()
		f.Logf("<- %s (%s)", msg, time.Since(start).Round(time.Millisecond))
	}
}

func randSuffix() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}

	return string(b)
}
