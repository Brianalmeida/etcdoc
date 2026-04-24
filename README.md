# etcd-cluster-reliability-tool

> **⚠️ Status: Experimental / Work In Progress (WIP)**
>
> This project is currently in an early development and research phase. It is not yet recommended for use in production environments.

## Overview & Architecture

Support teams frequently encounter situations where an etcd member is silently lagging or unhealthy before a full quorum loss event. Unfortunately, there is often no built-in mechanism in Rancher/RKE2 environments that surfaces this proactively.

The **etcd-cluster-reliability-tool** is a lightweight daemon designed to track critical etcd member health indicators, such as:
*   Database size growth rate
*   Disk fsync and backend commit latency
*   Leader election frequency

**Architecture:**
The tool is designed to run as a Kubernetes `DaemonSet` strictly on control-plane nodes. It locally scrapes the etcd `/metrics` endpoint, evaluates the data against predefined safety thresholds, and triggers external notifications (via Slack or PagerDuty) *before* catastrophic quorum loss occurs.

## Getting Started (Setup & Deployment)

### Prerequisites
*   A Kubernetes cluster (e.g., RKE2) with access to the control-plane nodes.
*   Access to the host filesystem where etcd TLS certificates are stored (default path for RKE2 is `/var/lib/rancher/rke2/server/tls/etcd/`).

### Deployment

A complete Kubernetes manifest is provided in `manifests/etcd-reliability-tool.yaml`.

1.  **Configure Secrets:**
    Before applying the manifest, you must configure the alerting secrets. Edit the `Secret` resource in the manifest or create one manually to include your tokens:
    ```yaml
    apiVersion: v1
    kind: Secret
    metadata:
      name: etcd-reliability-secrets
      namespace: kube-system
    type: Opaque
    stringData:
      SLACK_TOKEN: "your-slack-bot-token"
      PAGERDUTY_KEY: "your-pagerduty-integration-key"
    ```

2.  **Apply the Manifest:**
    ```bash
    kubectl apply -f manifests/etcd-reliability-tool.yaml
    ```
    This manifest deploys:
    *   A `ServiceAccount`, `ClusterRole`, and necessary RBAC.
    *   A `ConfigMap` for application settings and thresholds.
    *   A `DaemonSet` targeting nodes with the `node-role.kubernetes.io/control-plane` label.
    *   A `Service` and `ServiceMonitor` (for Prometheus Operator integration).

## Configuration & Metrics

### Configuration
The tool is configured via a `ConfigMap` (`etcd-reliability-config`). Key sections include:

*   **`etcd`**: Connection details for the local etcd instance, including paths to the mounted TLS certificates.
*   **`thresholds`**: The core evaluation logic limits.
    *   `fsync_latency_seconds`: Max allowed latency for disk fsync.
    *   `backend_commit_latency_seconds`: Max allowed latency for backend commits.
    *   `max_leader_changes_5m`: Max number of leader elections within a 5-minute window.
    *   `max_pending_proposals`: Max allowed pending raft proposals.
*   **`notifications`**: Toggle and configure integrations (Slack, PagerDuty).
*   **`observability`**: Enable or disable the internal `/metrics` endpoint.

### Internal Metrics & Observability
The tool exposes its own health and operational metrics on port `8080`:
*   **`/health`**: Returns the last evaluated state in JSON format.
*   **`/metrics`**: Exposes Prometheus metrics regarding the tool's internal operations (e.g., scrape errors, evaluation errors, and alerts dispatched).

If you are using the Prometheus Operator (e.g., Rancher Monitoring), a `ServiceMonitor` is included in the manifest to automatically scrape these internal metrics.

## Development

### Building Locally

To build the Go binary locally:
```bash
go build -o bin/etcd-reliability-tool ./cmd/etcd-reliability-tool
```

### Running Locally
To run the tool locally, you will need a `config.yaml` file and access to an etcd metrics endpoint.
```bash
./bin/etcd-reliability-tool -config path/to/config.yaml -interval 30s
```

### Docker Image
The manifest currently references an ephemeral `ttl.sh` image (`ttl.sh/etcd-reliability-tool-brian:5h`). To build and push your own image:

```bash
# Build the image
docker build -t your-registry/etcd-reliability-tool:latest .

# Push the image
docker push your-registry/etcd-reliability-tool:latest
```
*Note: Ensure you update the image reference in the `DaemonSet` manifest after pushing your custom image.*