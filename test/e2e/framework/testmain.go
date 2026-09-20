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
	"testing"
)

// TestMain does the one-time setup every e2e scenario package needs: build
// the shared cluster client from Config, sweep leftover namespaces from a
// killed previous run, then run the package's tests. Each scenario package
// (test/e2e/<scenario>/) calls this from its own TestMain function - Go
// requires TestMain to be declared per-package, so it can't be inherited,
// only delegated to:
//
//	func TestMain(m *testing.M) { os.Exit(framework.TestMain(m)) }
func TestMain(m *testing.M) int {
	cfg := LoadConfig()

	client, err := NewClient(cfg.Kubeconfig)
	if err != nil {
		log.Printf("e2e: failed to build cluster client: %v", err)

		return 1
	}

	SharedConfig = cfg
	SharedClient = client

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	log.Printf("e2e: sweeping leftover namespaces from previous runs (prefix %q)", cfg.NamespacePrefix)
	SweepLeftoverNamespaces(ctx, client.Clientset, cfg.NamespacePrefix)
	cancel()

	return m.Run()
}
