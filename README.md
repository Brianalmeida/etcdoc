# etcdoc -> Etcd Doctor

> **⚠️ Status: Experimental / Work In Progress (WIP)**
>
> This project is currently in an early development and research phase. It is not yet recommended for use in production environments.

## Overview & Architecture

Support teams frequently encounter situations where an etcd member is silently lagging or unhealthy before a full quorum loss event. Unfortunately, there is often no built-in mechanism in Rancher/RKE2 environments that surfaces this proactively.

**etcdoc** (formerly etcd-cluster-reliability-tool) is a lightweight daemon designed to track critical etcd member health indicators, such as:
*   Database size growth rate and quota utilization
*   Disk fsync and backend commit latency
*   Leader election frequency
*   Cluster size validations (supporting sizes up to 7 members)
*   Learner nodes and active defragmentation jobs

**Architecture:**
The tool is designed to run either as a Kubernetes `DaemonSet` on control-plane nodes, or as a standalone Local Container (via `docker-compose` or `nerdctl`). It locally scrapes the etcd `/metrics` endpoint, evaluates the data against predefined safety thresholds, and outputs actionable alerts to standard output (pod logs), the host filesystem, and as Prometheus metrics.

## Getting Started (Setup & Deployment)

### Prerequisites
*   An RKE2 environment with access to the host filesystem where etcd TLS certificates are stored (default path for RKE2 is `/var/lib/rancher/rke2/server/tls/etcd/`).

### Deployment Method 1: Kubernetes Helm Chart (Recommended)

etcdoc is packaged as a Helm chart for simple, parameterizable deployment.

```bash
# Install or upgrade the Helm chart
helm upgrade --install etcdoc ./manifests/chart/etcdoc -n kube-system
```
This chart deploys:
*   A `ServiceAccount`, `ClusterRole`, and `RoleBinding` for Kubernetes leader election.
*   A `ConfigMap` for application settings.
*   A `DaemonSet` (running as `root` to access host certificates) targeting nodes with the `node-role.kubernetes.io/control-plane` label.
*   A `Service` and `ServiceMonitor` (for Prometheus Operator integration) on port `8081`.

### Deployment Method 2: Local Container Run

If you need to bypass `kubectl` or run the tool on nodes directly, you can use `docker-compose` or `nerdctl`.

1. Review the included `docker-compose.yml` to ensure host paths map correctly.
2. Run the container:
```bash
docker-compose up -d
# or using nerdctl
nerdctl compose up -d
```

### Deployment Method 3: One-Shot Diagnostic Mode

For support engineers needing immediate answers on cluster health, `etcdoc` includes a one-shot diagnostic mode. This runs a single evaluation cycle, outputs the report, and exits with a semantic status code (0 for healthy, 1 for unhealthy). 

You can run this via `kubectl` if deployed, via `docker`/`nerdctl`, or directly on an RKE2 node using the packaged `ctr` binary, eliminating the need to install Docker.

```bash
# Via kubectl (if already deployed)
kubectl exec -it ds/etcdoc -n kube-system -- /etcdoc --once

# Via containerd (ctr) on an RKE2 node
sudo /var/lib/rancher/rke2/bin/ctr --address /run/k3s/containerd/containerd.sock --namespace k8s.io run --rm --net-host --user 0:0 \
  --mount type=bind,src=/var/lib/rancher/rke2/server/tls/etcd,dst=/var/lib/rancher/rke2/server/tls/etcd,options=rbind:ro \
  docker.io/library/etcdoc:local etcdoc-diag /etcdoc --once

# Or via docker/nerdctl
docker run --rm --network host \
  -v /var/lib/rancher/rke2/server/tls/etcd:/var/lib/rancher/rke2/server/tls/etcd:ro \
  etcdoc:local --once
```

## Logging & Automated Reporting

To facilitate easier support bundles, `etcdoc` employs a dual-write logging system:
1. **Instant Standard Logs**: Standard structured JSON logs (`slog`) stream directly to stdout/stderr (readable via `kubectl logs`).
2. **Host-Persisted Diagnostic Log**: Both standard logs AND a full diagnostic report are securely appended to the host at `/var/lib/rancher/rke2/server/tls/etcd/etcdoc-diag.log`.

A background timer automatically evaluates metrics and appends this detailed diagnostic report to the log file every 6 hours by default.

## Configuration & Metrics

### Configuration
The tool is configured via the Helm `values.yaml`, a local `config.yaml`, or Environment Variables. Key thresholds include:

*   `FSYNC_LATENCY_SECONDS`: Max allowed latency for disk fsync.
*   `BACKEND_COMMIT_LATENCY_SECONDS`: Max allowed latency for backend commits.
*   `MAX_LEADER_CHANGES_5M`: Max number of leader elections within a 5-minute window.
*   `MAX_PENDING_PROPOSALS`: Max allowed pending raft proposals.
*   `MAX_DB_SIZE_BYTES`: Max allowed database size in bytes (default 8GB). A WARN alert is triggered at 85% of this capacity.
*   `DIAGNOSTIC_INTERVAL`: How often the full diagnostic report is automatically appended to the log file (e.g., `6h`).

### Internal Metrics & Observability
The tool exposes its own health and operational metrics on port `8081` (chosen to avoid collisions on `hostNetwork`):
*   **`/health`**: Returns the last evaluated state in JSON format.
*   **`/metrics`**: Exposes Prometheus metrics regarding the tool's internal operations and dispatched alerts. Critical alerting now relies on external Alertmanager setups scraping these metrics.

### Deprecated Integrations (V2)
As of V2, the direct PagerDuty integration has been entirely removed from `etcdoc` to simplify its scope. Alert routing is now handled exclusively by outputting standard logs and exposing Prometheus metrics.

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
*Note: Ensure you update the image reference in the Helm `values.yaml` and `docker-compose.yml` after pushing your custom image.*