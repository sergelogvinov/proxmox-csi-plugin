# Metrics documentation

This document is a reflection of the current state of the exposed metrics of the Proxmox CSI controller.

## Gather metrics

Enabling the metrics is done by setting the `--metrics-address` flag to the desired address and port.

```yaml
proxmox-csi-controller
  --metrics-address=8080
```

### Helm chart values

The following values can be set in the Helm chart to expose the metrics of the Talos CCM.

```yaml
podAnnotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "8080"
```

## Metrics exposed by the CSI controller

### Proxmox API calls

|Metric name|Metric type|Labels/tags|
|-----------|-----------|-----------|
|proxmox_api_request_duration_seconds|Histogram|`request`=<api_request>|
|proxmox_api_request_errors_total|Counter|`request`=<api_request>|

Example output:

```txt
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="0.1"} 13
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="0.25"} 172
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="0.5"} 199
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="1"} 210
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="2.5"} 210
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="5"} 210
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="10"} 210
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="30"} 210
proxmox_api_request_duration_seconds_bucket{request="storageStatus",le="+Inf"} 210
proxmox_api_request_duration_seconds_sum{request="storageStatus"} 39.698945394000006
proxmox_api_request_duration_seconds_count{request="storageStatus"} 210
```
