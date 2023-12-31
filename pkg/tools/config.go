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

// Package tools implements tools to work with kubeernetes.
package tools

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// BuildConfig returns a kubernetes client configuration and namespace.
func BuildConfig(kubeconfig, namespace string) (k *rest.Config, ns string, err error) {
	clientConfigLoadingRules := clientcmd.NewDefaultClientConfigLoadingRules()

	if kubeconfig != "" {
		clientConfigLoadingRules.ExplicitPath = kubeconfig
	}

	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientConfigLoadingRules, &clientcmd.ConfigOverrides{})

	if namespace == "" {
		namespace, _, err = config.Namespace()
		if err != nil {
			return nil, "", fmt.Errorf("failed to get namespace from kubeconfig: %w", err)
		}
	}

	k, err = config.ClientConfig()

	return k, namespace, err
}
