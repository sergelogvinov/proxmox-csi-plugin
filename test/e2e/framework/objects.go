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
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// appLabelKey is the label used to select a StatefulSet's own pods, both for
// its Service/Selector and for the anti-affinity term spreading replicas
// across nodes.
const appLabelKey = "app"

// alpineImage is the test workload image shared by every pod builder in
// this file: small, and sleeps rather than exiting so a test can exec into
// it while its volume is mounted.
const alpineImage = "alpine"

// StatefulSetOptions parameterizes NewTestStatefulSet.
type StatefulSetOptions struct {
	Name         string
	Namespace    string
	StorageClass string
	Replicas     int32
	Size         string // e.g. "1Gi"
	Labels       map[string]string
}

// NewTestStatefulSet builds a StatefulSet + PVC-per-pod object mirroring
// docs/deploy/test-statefulset.yaml, parameterized for the e2e suite: an
// alpine container sleeping with a single volume mounted at /mnt, one PVC
// per replica via volumeClaimTemplates, and pod anti-affinity spreading
// replicas across nodes.
func NewTestStatefulSet(opts StatefulSetOptions) *appsv1.StatefulSet {
	labels := map[string]string{appLabelKey: opts.Name}
	for k, v := range opts.Labels {
		labels[k] = v
	}

	terminationGrace := int64(3)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			PodManagementPolicy: appsv1.ParallelPodManagement,
			ServiceName:         opts.Name,
			Replicas:            &opts.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{appLabelKey: opts.Name},
			},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &terminationGrace,
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									TopologyKey: "kubernetes.io/hostname",
									LabelSelector: &metav1.LabelSelector{
										MatchExpressions: []metav1.LabelSelectorRequirement{
											{
												Key:      appLabelKey,
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{opts.Name},
											},
										},
									},
								},
							},
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						FSGroup:    ptr.To(int64(1000)),
						RunAsUser:  ptr.To(int64(1000)),
						RunAsGroup: ptr.To(int64(1000)),
					},
					Containers: []corev1.Container{
						{
							Name:    alpineImage,
							Image:   alpineImage,
							Command: []string{"sleep", "1d"},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								RunAsUser:                ptr.To(int64(1000)),
								RunAsGroup:               ptr.To(int64(1000)),
								RunAsNonRoot:             ptr.To(true),
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "storage", MountPath: "/mnt"},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "storage"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: &opts.StorageClass,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(opts.Size),
							},
						},
					},
				},
			},
		},
	}
}

// NewNamespace builds a Namespace object labeled as belonging to the e2e suite.
func NewNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "proxmox-csi-plugin-e2e",
			},
		},
	}
}

// StatefulSetPVCName returns the name of the PVC Kubernetes generates for a
// given StatefulSet ordinal, e.g. "storage-test-0".
func StatefulSetPVCName(stsName string, ordinal int) string {
	return "storage-" + stsName + "-" + strconv.Itoa(ordinal)
}

// EphemeralPodOptions parameterizes NewEphemeralPod.
type EphemeralPodOptions struct {
	Name         string
	Namespace    string
	StorageClass string
	Size         string // e.g. "1Gi"
	NodeName     string // pins the pod via nodeSelector kubernetes.io/hostname
	VolumeName   string // defaults to "pvc" if empty
}

// NewEphemeralPod builds a Pod with a CSI generic ephemeral inline volume,
// mirroring docs/deploy/test-pod-secret-ephemeral.yaml: pinned to a specific
// node (so a test can find the colocated CSI node-plugin pod), non-root, a
// sleeping alpine container with the volume mounted at /mnt.
func NewEphemeralPod(opts EphemeralPodOptions) *corev1.Pod {
	volumeName := opts.VolumeName
	if volumeName == "" {
		volumeName = "pvc"
	}

	terminationGrace := int64(1)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &terminationGrace,
			Tolerations: []corev1.Toleration{
				{Effect: corev1.TaintEffectNoSchedule, Key: "node-role.kubernetes.io/control-plane"},
			},
			NodeSelector: map[string]string{"kubernetes.io/hostname": opts.NodeName},
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup:    ptr.To(int64(65534)),
				RunAsGroup: ptr.To(int64(65534)),
				RunAsUser:  ptr.To(int64(65534)),
			},
			Containers: []corev1.Container{
				{
					Name:    alpineImage,
					Image:   alpineImage,
					Command: []string{"sleep", "6000"},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						RunAsNonRoot:             ptr.To(true),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: volumeName, MountPath: "/mnt"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: volumeName,
					VolumeSource: corev1.VolumeSource{
						Ephemeral: &corev1.EphemeralVolumeSource{
							VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
								ObjectMeta: metav1.ObjectMeta{
									Labels: map[string]string{"type": "pvc-volume"},
								},
								Spec: corev1.PersistentVolumeClaimSpec{
									AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
									StorageClassName: &opts.StorageClass,
									Resources: corev1.VolumeResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceStorage: resource.MustParse(opts.Size),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// EphemeralPVCName returns the name Kubernetes generates for a generic
// ephemeral volume's PVC: "<pod name>-<volume name>".
func EphemeralPVCName(podName, volumeName string) string {
	return podName + "-" + volumeName
}

// NewVolumeAttributesClass builds a VolumeAttributesClass, mirroring
// docs/volume-attributes.yaml: a cluster-scoped object naming the CSI driver
// and the mutable Proxmox disk parameters (backup, diskIOPS, diskMBps, ...)
// it should apply. Unlike a StorageClass, the e2e suite owns the lifecycle
// of the ones it creates - they're throwaway parameter sets specific to a
// test run, not a durable mapping to a Proxmox storage backend - so callers
// must register their own cleanup.
func NewVolumeAttributesClass(name, driverName string, parameters map[string]string) *storagev1.VolumeAttributesClass {
	return &storagev1.VolumeAttributesClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		DriverName: driverName,
		Parameters: parameters,
	}
}
