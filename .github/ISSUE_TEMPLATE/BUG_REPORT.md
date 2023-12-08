---
name: Bug Report
about: Report a bug.
title: ""
labels: ""
assignees: ""
---

## Bug Report

### Description

### Logs

Controller: [`kubectl logs -c proxmox-csi-plugin-controller proxmox-csi-plugin-controller-...`]

Node: [`kubectl logs -c proxmox-csi-plugin-node proxmox-csi-plugin-node-...`]

### Environment

- Plugin version:
- Kubernetes version: [`kubectl version --short`]
- CSI capasity: [`kubectl get csistoragecapacities -ocustom-columns=CLASS:.storageClassName,AVAIL:.capacity,ZONE:.nodeTopology.matchLabels -A`]
- CSI resource on the node: [`kubectl get CSINode <node> -oyaml`]
- Node describe: [`kubectl describe node <node>`]
- OS version [`cat /etc/os-release`]
