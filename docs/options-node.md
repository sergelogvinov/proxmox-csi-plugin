# Node Annotations and Labels

Individual Kubernetes Nodes can be configured by annotations and labels to provide additional information to the Proxmox CSI Driver.
Most of these settings are optional.

```yaml
apiVersion: v1
kind: Node
metadata:
  ...
  annotations:
    # Proxmox Virtual Machine ID to help identify the node (optional)
    proxmox.sinextra.dev/instance-id: "VM-ID"

  labels:
    # Topology labels to override default node topology labels (optional)
    # Note: Kubernetes scheduler uses only default topology labels - topology.kubernetes.io/region and topology.kubernetes.io/zone
    topology.proxmox.sinextra.dev/region: cluster-1
    topology.proxmox.sinextra.dev/zone: pve-node-1

    # Default Kubernetes topology labels
    topology.kubernetes.io/region: cluster-1
    topology.kubernetes.io/zone: pve-node-1

    # Maximum number of volumes that can be attached to this node, default is 24 (optional)
    # Note: Currently, there is a maximum limit of 30 virtio iscsi volumes *total*, including root disks, that can be attached to a single VM in QEMU/Proxmox.
    csi.proxmox.sinextra.dev/max-volume-attachments: "24"
...
spec:
  # Provider ID to help identify the node in the Proxmox cluster (optional)
  # Format: proxmox://<cluster-name>/<VM-ID>
  #
  # Note: This field is optional but recommended for better identification of the node.
  # Or you can set SMBIOS custom fields in Cloud-Init configuration to set the Proxmox VM ID.
  providerID: proxmox://cluster-1/VM-ID
```

## Cloud-Init SMBIOS custom fields

Also you can use Cloud-Init SMBIOS custom fields to set the Proxmox VM ID during the node creation.
Proxmox VM ID is very important for the Proxmox CSI Driver to identify nodes correctly.
If it sets properly, the plugin will work more reliably and faster.

Proxmox VM configuration example:

```yaml
smbios1: base64=1,serial=aD13ZWItMDJhO2k9MTIwMjA=,uuid=de5b02b0-828a-4d1d-b135-2b812aa7d943
```

Where `serial` field is base64 encoded string of `aD13ZWItMDJhO2k9MTIwMjA=` which decodes to `h=web-02a;i=12020`, where `i` is the VM ID.
More information about Proxmox Cloud-Init SMBIOS fields is available in the [Cloud-Init SMBIOS documentation](https://cloudinit.readthedocs.io/en/latest/reference/datasources/nocloud.html#dmi-specific-kernel-command-line).

Terraform [Proxmox provider](https://registry.terraform.io/providers/bpg/proxmox/latest/docs/resources/virtual_environment_vm) example:

```hcl
resource "proxmox_virtual_environment_vm" "k8s-nodes" {
  node_name           = each.value.zone
  vm_id               = each.value.id

  smbios {
    serial = "h=${each.value.zone};i=${each.value.id}"
  }
}
```
