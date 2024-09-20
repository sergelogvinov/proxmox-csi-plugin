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

### Community Note

* Please vote on this issue by adding a 👍 reaction to the original issue to help the community and maintainers prioritize this request
* Please do not leave "+1" or other comments that do not add relevant new information or questions, they generate extra noise for issue followers and do not help prioritize the request
