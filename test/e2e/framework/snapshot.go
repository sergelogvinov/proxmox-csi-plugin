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

	volume "github.com/sergelogvinov/proxmox-csi-plugin/pkg/utils/volume"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

// volumeSnapshotAPIVersion is the apiVersion every VolumeSnapshot* object
// built in this file carries.
const volumeSnapshotAPIVersion = VolumeSnapshotAPIGroup + "/v1"

// VolumeSnapshot* GroupVersionResources (snapshot.storage.k8s.io/v1, the
// external-snapshotter CRDs). The suite talks to these through the dynamic
// client rather than adding
// github.com/kubernetes-csi/external-snapshotter/client as a dependency just
// for types - see docs/e2e.md's Open questions.
var (
	VolumeSnapshotClassGVR   = schema.GroupVersionResource{Group: VolumeSnapshotAPIGroup, Version: "v1", Resource: "volumesnapshotclasses"}
	VolumeSnapshotGVR        = schema.GroupVersionResource{Group: VolumeSnapshotAPIGroup, Version: "v1", Resource: "volumesnapshots"}
	VolumeSnapshotContentGVR = schema.GroupVersionResource{Group: VolumeSnapshotAPIGroup, Version: "v1", Resource: "volumesnapshotcontents"}
)

// NewVolumeSnapshotClass builds a cluster-scoped VolumeSnapshotClass,
// mirroring docs/volumesnapshot.md. deletionPolicy is always "Delete" - the
// suite owns whatever VolumeSnapshotClass it creates, and a Delete policy is
// what proves cleanup actually removes the backing Proxmox disk copy instead
// of leaving one orphaned. parameters may be nil (e.g. "zone" to target a
// specific Proxmox zone for the snapshot copy - see docs/volumesnapshot.md).
func NewVolumeSnapshotClass(name, driverName string, parameters map[string]string) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": volumeSnapshotAPIVersion,
		"kind":       "VolumeSnapshotClass",
		"metadata": map[string]any{
			"name": name,
		},
		"driver":         driverName,
		"deletionPolicy": "Delete",
	}

	if len(parameters) > 0 {
		params := make(map[string]any, len(parameters))
		for k, v := range parameters {
			params[k] = v
		}

		obj["parameters"] = params
	}

	return &unstructured.Unstructured{Object: obj}
}

// NewVolumeSnapshot builds a namespaced VolumeSnapshot of sourcePVCName under
// the given VolumeSnapshotClass.
func NewVolumeSnapshot(name, namespace, className, sourcePVCName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": volumeSnapshotAPIVersion,
		"kind":       "VolumeSnapshot",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"volumeSnapshotClassName": className,
			"source": map[string]any{
				"persistentVolumeClaimName": sourcePVCName,
			},
		},
	}}
}

// VolumeSnapshotStatus is the subset of a VolumeSnapshot's status this suite
// inspects.
type VolumeSnapshotStatus struct {
	ReadyToUse                     bool
	RestoreSize                    *resource.Quantity
	BoundVolumeSnapshotContentName string
}

// WaitForVolumeSnapshotReady polls until the named VolumeSnapshot reports
// status.readyToUse: true, and returns its status. An Error condition in
// status.error is a terminal failure (e.g. an unsupported storage backend),
// so the wait aborts immediately instead of polling until timeout.
func WaitForVolumeSnapshotReady(ctx context.Context, dynamicClient dynamic.Interface, namespace, name string, timeout time.Duration) (*VolumeSnapshotStatus, error) {
	var status *VolumeSnapshotStatus

	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dynamicClient.Resource(VolumeSnapshotGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case isTransientError(err):
			return false, nil
		case err != nil:
			return false, err
		}

		ready, _, err := unstructured.NestedBool(obj.Object, "status", "readyToUse")
		if err != nil {
			return false, fmt.Errorf("volumesnapshot %s/%s has a malformed status.readyToUse: %w", namespace, name, err)
		}

		contentName, _, err := unstructured.NestedString(obj.Object, "status", "boundVolumeSnapshotContentName")
		if err != nil {
			return false, fmt.Errorf("volumesnapshot %s/%s has a malformed status.boundVolumeSnapshotContentName: %w", namespace, name, err)
		}

		s := &VolumeSnapshotStatus{ReadyToUse: ready, BoundVolumeSnapshotContentName: contentName}

		sizeStr, found, err := unstructured.NestedString(obj.Object, "status", "restoreSize")
		if err != nil {
			return false, fmt.Errorf("volumesnapshot %s/%s has a malformed status.restoreSize: %w", namespace, name, err)
		}

		if found && sizeStr != "" {
			q, err := resource.ParseQuantity(sizeStr)
			if err != nil {
				return false, fmt.Errorf("volumesnapshot %s/%s has unparseable status.restoreSize %q: %w", namespace, name, sizeStr, err)
			}

			s.RestoreSize = &q
		}

		msg, found, err := unstructured.NestedString(obj.Object, "status", "error", "message")
		if err != nil {
			return false, fmt.Errorf("volumesnapshot %s/%s has a malformed status.error.message: %w", namespace, name, err)
		}

		if found && msg != "" {
			return false, fmt.Errorf("volumesnapshot %s/%s reported an error: %s", namespace, name, msg)
		}

		status = s

		return ready, nil
	})
	if err != nil {
		return status, fmt.Errorf("volumesnapshot %s/%s did not become ready: %w", namespace, name, err)
	}

	return status, nil
}

// VolumeSnapshotContentHandle returns the driver's status.snapshotHandle for
// the named VolumeSnapshotContent - the CSI snapshot ID
// (region/zone/storage/disk, the same shape as a PV's volume handle - see
// pkg/utils/volume) a csi-snapshotter sets once CreateSnapshot succeeds.
func VolumeSnapshotContentHandle(ctx context.Context, dynamicClient dynamic.Interface, name string) (string, error) {
	obj, err := dynamicClient.Resource(VolumeSnapshotContentGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get volumesnapshotcontent %s: %w", name, err)
	}

	handle, found, err := unstructured.NestedString(obj.Object, "status", "snapshotHandle")
	if err != nil {
		return "", fmt.Errorf("volumesnapshotcontent %s has a malformed status.snapshotHandle: %w", name, err)
	}

	if !found || handle == "" {
		return "", fmt.Errorf("volumesnapshotcontent %s has no status.snapshotHandle yet", name)
	}

	return handle, nil
}

// VolumeZone returns the Proxmox zone encoded in a CSI volume or snapshot
// handle (region/zone/storage/disk - see pkg/utils/volume), used to compare
// where a PV's volume vs. a VolumeSnapshotContent's copy actually landed.
func VolumeZone(handle string) (string, error) {
	vol, err := volume.NewVolumeFromVolumeID(handle)
	if err != nil {
		return "", fmt.Errorf("failed to parse volume handle %q: %w", handle, err)
	}

	return vol.Zone(), nil
}

// WaitForVolumeSnapshotContentGone polls until the named
// VolumeSnapshotContent no longer exists - the real assertion that a Delete
// deletionPolicy actually removed the backing Proxmox disk copy, not just
// that the namespaced VolumeSnapshot object was deleted.
func WaitForVolumeSnapshotContentGone(ctx context.Context, dynamicClient dynamic.Interface, name string, timeout time.Duration) error {
	err := waitForGone(ctx, timeout, func(ctx context.Context) error {
		_, err := dynamicClient.Resource(VolumeSnapshotContentGVR).Get(ctx, name, metav1.GetOptions{})

		return err
	})
	if err != nil {
		return fmt.Errorf("volumesnapshotcontent %q was not deleted: %w", name, err)
	}

	return nil
}
