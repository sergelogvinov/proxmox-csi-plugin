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

// Package framework provides the plumbing (clients, waiters, object builders)
// shared by the proxmox-csi-plugin end-to-end tests. See docs/e2e.md for the
// test plan and the assumptions the suite makes about the target cluster.
package framework

import (
	"log"
	"os"
	"strings"
	"time"
)

// Config holds the e2e suite configuration, read from environment variables.
//
// The suite never deploys the CSI driver or StorageClasses itself: the
// target cluster (selected via KUBECONFIG) is expected to already have both.
type Config struct {
	// Kubeconfig is the path to the kubeconfig of the cluster under test.
	// Empty means "use client-go's default loading rules" (KUBECONFIG env,
	// then ~/.kube/config).
	Kubeconfig string

	// StorageClass is the primary StorageClass used by tests that don't
	// care about backend specifics (lifecycle, expansion, snapshot, ...).
	StorageClass string

	// StorageClasses is the set of StorageClasses exercised by the backend
	// matrix test.
	StorageClasses []string

	// NamespacePrefix namespaces created by the suite are named
	// "<prefix>-<random>" and removed on cleanup.
	NamespacePrefix string

	// Timeout is the default per-wait timeout (pod ready, PVC bound, PV
	// gone, snapshot ready, ...).
	Timeout time.Duration

	// ProxmoxConfig, when set, points at a cloud-config.yaml the suite can
	// use to talk to the Proxmox API directly (via go-proxmox-pool) to
	// verify backing disks independently of what Kubernetes reports.
	ProxmoxConfig string

	// EncryptedStorageClass is the StorageClass used by the encrypted-volume
	// test. It must carry the standard CSI node-stage-secret parameters
	// (csi.storage.k8s.io/node-stage-secret-name/-namespace) pointing at a
	// Secret with an "encryption-passphrase" key - see hack/e2e-tests.md.
	EncryptedStorageClass string

	// ReplicatedStorageClass is the StorageClass used by the
	// zonereplication test. It must be a ZFS-backed class with
	// parameters.replicate: "true" and parameters.replicateZones listing
	// exactly two Proxmox zones (see docs/options.md) - the test reads
	// those two zones directly off the StorageClass rather than a
	// separately configured env var.
	ReplicatedStorageClass string

	// SharedStorageClass is the StorageClass used by the shared-storage
	// test. It must be backed by a Proxmox storage marked "Shared" and
	// accessible from every node in the region (no per-node restriction),
	// e.g. Ceph/RBD/NFS - see docs/install.md.
	SharedStorageClass string

	// NodeName, when set, pins the encrypted-volume test to a specific
	// node instead of auto-selecting the first Ready/schedulable one.
	NodeName string

	// SnapshotZone, when set, overrides the Proxmox zone targets via
	// VolumeSnapshotClass.parameters.zone. Left unset, that scenario
	// auto-discovers a zone distinct from the source volume's own from
	// nodes' topology.kubernetes.io/zone labels, within the same
	// topology.kubernetes.io/region
	SnapshotZone string

	// NodePluginNamespace/NodePluginLabelSelector locate the CSI
	// node-plugin DaemonSet pods, used to inspect host-level state
	// (dm-crypt mappers) that isn't visible from inside a workload pod.
	NodePluginNamespace     string
	NodePluginLabelSelector string
	NodePluginContainer     string
}

// LoadConfig builds a Config from environment variables, applying defaults
// documented in docs/e2e.md.
func LoadConfig() Config {
	cfg := Config{
		Kubeconfig:              os.Getenv("KUBECONFIG"),
		NamespacePrefix:         getEnvDefault("E2E_NAMESPACE_PREFIX", "e2e"),
		StorageClass:            getEnvDefault("E2E_STORAGECLASS", "proxmox"),
		EncryptedStorageClass:   getEnvDefault("E2E_ENCRYPTED_STORAGECLASS", "proxmox-secret"),
		ReplicatedStorageClass:  getEnvDefault("E2E_REPLICATED_STORAGECLASS", "proxmox-zfs"),
		SharedStorageClass:      getEnvDefault("E2E_SHARED_STORAGECLASS", "proxmox-ceph"),
		ProxmoxConfig:           os.Getenv("E2E_PROXMOX_CONFIG"),
		NodeName:                os.Getenv("E2E_NODE_NAME"),
		SnapshotZone:            os.Getenv("E2E_SNAPSHOT_ZONE"),
		NodePluginNamespace:     getEnvDefault("E2E_NODE_NAMESPACE", "csi-proxmox"),
		NodePluginLabelSelector: getEnvDefault("E2E_NODE_LABEL_SELECTOR", "app.kubernetes.io/name=proxmox-csi-plugin,app.kubernetes.io/component=node"),
		NodePluginContainer:     getEnvDefault("E2E_NODE_CONTAINER", "proxmox-csi-plugin-node"),
		Timeout:                 getEnvDurationDefault("E2E_TIMEOUT", 5*time.Minute),
	}

	classes := getEnvDefault("E2E_STORAGECLASSES", "proxmox,proxmox-ceph,proxmox-rbd")
	for c := range strings.SplitSeq(classes, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cfg.StorageClasses = append(cfg.StorageClasses, c)
		}
	}

	return cfg
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return def
}

func getEnvDurationDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}

		log.Printf("e2e: invalid %s=%q (%v), using default %s", key, v, err, def)
	}

	return def
}
