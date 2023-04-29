# proxmox-csi-plugin

![Version: 0.0.1](https://img.shields.io/badge/Version-0.0.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.1](https://img.shields.io/badge/AppVersion-0.0.1-informational?style=flat-square)

A CSI plugin for Proxmox

**Homepage:** <https://github.com/sergelogvinov/proxmox-csi-plugin>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| sergelogvinov |  |  |

## Source Code

* <https://github.com/sergelogvinov/proxmox-csi-plugin>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| clusterID | string | `"kubernetes"` |  |
| config.clusters | list | `[]` |  |
| configFile | string | `"/etc/proxmox/config.yaml"` |  |
| controller.attacher.image.pullPolicy | string | `"IfNotPresent"` |  |
| controller.attacher.image.repository | string | `"registry.k8s.io/sig-storage/csi-attacher"` |  |
| controller.attacher.image.tag | string | `"v4.2.0"` |  |
| controller.attacher.resources.requests.cpu | string | `"10m"` |  |
| controller.attacher.resources.requests.memory | string | `"16Mi"` |  |
| controller.plugin.image.pullPolicy | string | `"IfNotPresent"` |  |
| controller.plugin.image.repository | string | `"ghcr.io/sergelogvinov/proxmox-csi-controller"` |  |
| controller.plugin.image.tag | string | `""` |  |
| controller.plugin.resources.requests.cpu | string | `"10m"` |  |
| controller.plugin.resources.requests.memory | string | `"16Mi"` |  |
| controller.provisioner.image.pullPolicy | string | `"IfNotPresent"` |  |
| controller.provisioner.image.repository | string | `"registry.k8s.io/sig-storage/csi-provisioner"` |  |
| controller.provisioner.image.tag | string | `"v3.4.0"` |  |
| controller.provisioner.resources.requests.cpu | string | `"10m"` |  |
| controller.provisioner.resources.requests.memory | string | `"16Mi"` |  |
| controller.resizer.image.pullPolicy | string | `"IfNotPresent"` |  |
| controller.resizer.image.repository | string | `"registry.k8s.io/sig-storage/csi-resizer"` |  |
| controller.resizer.image.tag | string | `"v1.7.0"` |  |
| controller.resizer.resources.requests.cpu | string | `"10m"` |  |
| controller.resizer.resources.requests.memory | string | `"16Mi"` |  |
| existingConfigSecret | string | `nil` |  |
| existingConfigSecretKey | string | `"config.yaml"` |  |
| fullnameOverride | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| livenessprobe.failureThreshold | int | `5` |  |
| livenessprobe.image.pullPolicy | string | `"IfNotPresent"` |  |
| livenessprobe.image.repository | string | `"registry.k8s.io/sig-storage/livenessprobe"` |  |
| livenessprobe.image.tag | string | `"v2.9.0"` |  |
| livenessprobe.initialDelaySeconds | int | `10` |  |
| livenessprobe.periodSeconds | int | `60` |  |
| livenessprobe.resources.requests.cpu | string | `"10m"` |  |
| livenessprobe.resources.requests.memory | string | `"16Mi"` |  |
| livenessprobe.timeoutSeconds | int | `10` |  |
| logVerbosityLevel | int | `5` |  |
| nameOverride | string | `""` |  |
| node.driverRegistrar.image.pullPolicy | string | `"IfNotPresent"` |  |
| node.driverRegistrar.image.repository | string | `"registry.k8s.io/sig-storage/csi-node-driver-registrar"` |  |
| node.driverRegistrar.image.tag | string | `"v2.7.0"` |  |
| node.driverRegistrar.resources.requests.cpu | string | `"10m"` |  |
| node.driverRegistrar.resources.requests.memory | string | `"16Mi"` |  |
| node.nodeSelector | object | `{}` |  |
| node.plugin.image.pullPolicy | string | `"IfNotPresent"` |  |
| node.plugin.image.repository | string | `"ghcr.io/sergelogvinov/proxmox-csi-node"` |  |
| node.plugin.image.tag | string | `""` |  |
| node.plugin.resources | object | `{}` |  |
| node.tolerations[0].effect | string | `"NoSchedule"` |  |
| node.tolerations[0].key | string | `"node.kubernetes.io/unschedulable"` |  |
| node.tolerations[0].operator | string | `"Exists"` |  |
| node.tolerations[1].effect | string | `"NoSchedule"` |  |
| node.tolerations[1].key | string | `"node.kubernetes.io/disk-pressure"` |  |
| node.tolerations[1].operator | string | `"Exists"` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podSecurityContext.fsGroup | int | `65532` |  |
| podSecurityContext.fsGroupChangePolicy | string | `"OnRootMismatch"` |  |
| podSecurityContext.runAsGroup | int | `65532` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `65532` |  |
| priorityClassName | string | `"system-cluster-critical"` |  |
| provisionerName | string | `"csi.proxmox.sinextra.dev"` |  |
| replicaCount | int | `1` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| storageClass | list | `[]` |  |
| timeout | string | `"3m"` |  |
| tolerations | list | `[]` |  |
| updateStrategy.rollingUpdate.maxUnavailable | int | `1` |  |
| updateStrategy.type | string | `"RollingUpdate"` |  |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.11.0](https://github.com/norwoodj/helm-docs/releases/v1.11.0)
