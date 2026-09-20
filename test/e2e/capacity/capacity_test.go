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

package capacity

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/proxmox-csi-plugin/test/e2e/framework"
)

// TestCSIStorageCapacityExists confirms the driver actually publishes a
// CSIStorageCapacity object for f.Config.StorageClass. That object is what
// the scheduler consults for capacity-aware placement (the driver's
// CSIDriver has spec.storageCapacity: true, and its controller runs with
// --enable-capacity - see charts/proxmox-csi-plugin/templates/csidriver.yaml
// and controller-deployment.yaml): if it's missing, new volumes on this
// StorageClass with late binding can be scheduled onto nodes the driver
// can't actually provision for, or (per the CSIStorageCapacity API doc)
// the scheduler may treat the class as having no capacity at all.
func TestCSIStorageCapacityExists(t *testing.T) {
	f := framework.New(t)
	require := require.New(t)

	f.Logf("checking for a csistoragecapacity for storageclass %s in namespace %s", f.Config.StorageClass, f.Config.NodePluginNamespace)

	done := f.Step("waiting for a csistoragecapacity for storageclass %s", f.Config.StorageClass)
	ctx, cancel := f.Context()
	capacity, err := framework.WaitForCSIStorageCapacity(ctx, f.Client.Clientset, f.Config.NodePluginNamespace, f.Config.StorageClass, f.Config.Timeout)

	cancel()
	done()
	require.NoError(err, "no csistoragecapacity for storageclass %s in namespace %s - is --enable-capacity set and CSIDriver.spec.storageCapacity true?",
		f.Config.StorageClass, f.Config.NodePluginNamespace)

	require.Equal(f.Config.StorageClass, capacity.StorageClassName)

	switch capacity.Capacity {
	case nil:
		f.Logf("csistoragecapacity %s exists for storageclass %s but reports no capacity value yet (nil means \"unknown\" per the API, not a failure)", capacity.Name, f.Config.StorageClass)
	default:
		f.Logf("csistoragecapacity %s reports %s available for storageclass %s", capacity.Name, capacity.Capacity.String(), f.Config.StorageClass)
	}
}
