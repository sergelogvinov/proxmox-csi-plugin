# Plugin configuration file

This file is used to configure the Proxmox CSI driver plugin.

```yaml
features:
  # Provider type
  provider: default|capmox

clusters:
  # List of Proxmox clusters
  - url: https://cluster-api-1.exmple.com:8006/api2/json
    # Skip the certificate verification, if needed
    insecure: false
    # Proxmox api token
    token_id: "kubernetes-csi@pve!csi"
    token_id_file: "/etc/proxmox/token_id"          # Optional, alternative to token_id
    token_secret: "secret"
    token_secret_file: "/etc/proxmox/token_secret"  # Optional, alternative to token_secret
    # Region name, which is cluster name
    region: Region-1

  # Add more clusters if needed
  - url: https://cluster-api-2.exmple.com:8006/api2/json
    insecure: false
    token_id: "kubernetes-csi@pve!csi"
    token_secret: "secret"
    region: Region-2
```

## Cluster list

You can define multiple clusters in the `clusters` section.

* `url` - The URL of the Proxmox cluster API.
* `insecure` - Set to `true` to skip TLS certificate verification.
* `token_id` - The Proxmox API token ID.
* `token_id_file` - The path to a file containing the Proxmox API token ID. This is an alternative to `token_id`.
* `token_secret` - The name of the Kubernetes Secret that contains the Proxmox API token.
* `token_secret_file` - The path to a file containing the Proxmox API token secret. This is an alternative to `token_secret`.
* `region` - The name of the region, which is also used as `topology.kubernetes.io/region` label.

## Feature flags

* `provider` - Set the provider type. The default is `default`, which uses provider-id to define the Proxmox VM ID. The `capmox` value is used for working with the Cluster API for Proxmox (CAPMox).
