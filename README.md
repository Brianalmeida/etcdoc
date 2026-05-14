# etcdoc

> **⚠️ Status: Experimental / Work In Progress (WIP)**
>
> This project is currently in an early development and research phase. It is not yet recommended for use in production environments.

## Overview & Architecture

Support teams frequently encounter situations where an etcd member is silently lagging or unhealthy before a full quorum loss event. Unfortunately, there is often no built-in mechanism in Rancher/RKE2 environments that surfaces this proactively.

**etcdoc** (formerly etcd-cluster-reliability-tool) is a lightweight daemon designed to track critical etcd member health indicators, such as:
*   Database size growth rate
*   Disk fsync and backend commit latency
*   Leader election frequency

**Architecture:**
The tool is designed to run either as a Kubernetes `DaemonSet` on control-plane nodes, or as a standalone Local Container (via `docker-compose` or `nerdctl`). It locally scrapes the etcd `/metrics` endpoint, evaluates the data against predefined safety thresholds, and outputs actionable alerts to standard output (pod logs) and as Prometheus metrics.

## Getting Started (Setup & Deployment)

### Prerequisites
*   An RKE2 environment with access to the host filesystem where etcd TLS certificates are stored (default path for RKE2 is `/var/lib/rancher/rke2/server/tls/etcd/`).

### Deployment Method 1: Kubernetes DaemonSet (Recommended)

A complete Kubernetes manifest is provided in `manifests/etcdoc.yaml`.

```bash
kubectl apply -f manifests/etcdoc.yaml
```
This manifest deploys:
*   A `ServiceAccount`, `ClusterRole`, and `RoleBinding` for Kubernetes leader election.
*   A `ConfigMap` for application settings.
*   A `DaemonSet` targeting nodes with the `node-role.kubernetes.io/control-plane` label.
*   A `Service` and `ServiceMonitor` (for Prometheus Operator integration).

### Deployment Method 2: Local Container Run

If you need to bypass `kubectl` or run the tool on nodes directly, you can use `docker-compose` or `nerdctl`.

1. Review the included `docker-compose.yml` to ensure host paths (like `/var/lib/rancher/rke2/server/tls/etcd/`) map correctly.
2. Run the container:
```bash
docker-compose up -d
# or using nerdctl
nerdctl compose up -d
```

### Deployment Method 3: One-Shot Diagnostic Mode

For support engineers needing immediate answers on cluster health, `etcdoc` includes a one-shot diagnostic mode. This runs a single evaluation cycle and exits with a semantic status code (0 for healthy, 1 for unhealthy).

```bash
# Via docker/nerdctl
docker run --rm --network host \
  -v /var/lib/rancher/rke2/server/tls/etcd:/var/lib/rancher/rke2/server/tls/etcd:ro \
  etcdoc:local --once
```

## Configuration & Metrics

### Configuration
The tool is configured via `config.yaml` or Environment Variables. Key thresholds include:

*   `FSYNC_LATENCY_SECONDS`: Max allowed latency for disk fsync.
*   `BACKEND_COMMIT_LATENCY_SECONDS`: Max allowed latency for backend commits.
*   `MAX_LEADER_CHANGES_5M`: Max number of leader elections within a 5-minute window.
*   `MAX_PENDING_PROPOSALS`: Max allowed pending raft proposals.
*   `MAX_DB_SIZE_BYTES`: Max allowed database size in bytes (default 8GB).

### Internal Metrics & Observability
The tool exposes its own health and operational metrics on port `8080`:
*   **`/health`**: Returns the last evaluated state in JSON format.
*   **`/metrics`**: Exposes Prometheus metrics regarding the tool's internal operations and dispatched alerts. Critical alerting now relies on external Alertmanager setups scraping these metrics.

## Development

### Building Locally

To build the Go binary locally:
```bash
go build -o bin/etcdoc ./cmd/etcdoc
```

### Docker Image
To build and push your own image:

```bash
# Build the image
docker build -t your-registry/etcdoc:latest .

# Push the image
docker push your-registry/etcdoc:latest
```
*Note: Ensure you update the image reference in the `DaemonSet` manifest and `docker-compose.yml` after pushing your custom image.*