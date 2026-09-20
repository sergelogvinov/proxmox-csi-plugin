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
	"fmt"

	tools "github.com/sergelogvinov/proxmox-csi-plugin/pkg/tools/kubernetes"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client bundles the clients the e2e suite needs against the cluster under test.
type Client struct {
	RESTConfig *rest.Config
	Clientset  *kubernetes.Clientset
	Dynamic    dynamic.Interface
}

// NewClient builds a Client from the given kubeconfig path (empty uses
// client-go's default loading rules: $KUBECONFIG, then ~/.kube/config).
func NewClient(kubeconfig string) (*Client, error) {
	restConfig, _, err := tools.BuildConfig(kubeconfig, "default")
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build dynamic client: %w", err)
	}

	return &Client{
		RESTConfig: restConfig,
		Clientset:  clientset,
		Dynamic:    dynamicClient,
	}, nil
}
