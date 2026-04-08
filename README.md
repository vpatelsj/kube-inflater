# kube-inflater

Kubernetes scalability and stress-testing toolkit. Bulk-create API resources at rates exceeding 200/sec, inflate clusters with hundreds of thousands of hollow kubemark nodes, stress-test the watch subsystem to 100k+ concurrent connections, benchmark every API endpoint, and probe etcd directly — all from a single repo with a unified web UI.

```
┌──────────────────────────────────────────────────────────────────┐
│                         kube-inflater                            │
├──────────┬────────────┬────────────┬─────────────┬──────────────┤
│ inflater │watch-agent │ perf-report│cleanup-nodes│ benchmark-ui │
│ engine   │ watch+     │            │             │  (React/Go)  │
│          │ mutate     │            │             │              │
├──────────┴────┬───────┴──────┬─────┴─────┬───────┴──────────────┤
│  resourcegen  │  watchstress │   perfv2  │     benchmarkio      │
│  (10 types)   │              │           │   (3 report types)   │
├───────────────┼──────────────┤           ├──────────────────────┤
│ plan · kwok · │  clustermon  │           │    etcd-tester       │
│ daemonsetspec │              │           │  (separate module)   │
└───────────────┴──────────────┴───────────┴──────────────────────┘
```

---

## Table of Contents

- [Quick Start](#quick-start)
- [Tools](#tools)
- [kube-inflater — Resource Inflater](#kube-inflater--resource-inflater)
- [watch-agent — Watch Stress Testing](#watch-agent--watch-stress-testing)
- [benchmark-ui — Web Dashboard](#benchmark-ui--web-dashboard)
- [perf-report — API Latency Reports](#perf-report--api-latency-reports)
- [cleanup-nodes — Node & Pod Cleanup](#cleanup-nodes--node--pod-cleanup)
- [analyze-notready — NotReady Node Inspector](#analyze-notready--notready-node-inspector)
- [etcd-tester — etcd Probe](#etcd-tester--etcd-probe)
- [Core Concepts](#core-concepts)
- [Project Layout](#project-layout)
- [Build Targets](#build-targets-mage)
- [Requirements](#requirements)
- [Debugging](#debugging)

---

## Quick Start

```bash
# Install Mage build tool
go install github.com/magefile/mage@latest

# Build the unified binary
mage build                  # → bin/kube-inflater

# Create 1000 ConfigMaps across 10 namespaces
./bin/kube-inflater --resource-types=configmaps --count=1000

# Create 100k pods on KWOK fake nodes (auto-provisioned)
./bin/kube-inflater --resource-types=pods --count=100000 \
  --workers=200 --qps=500 --burst=1000

# Open 100 concurrent watch connections for 60 seconds
mage build && ./bin/watch-agent watch --connections=100 --duration=60

# Launch the web UI
mage benchmarkUI && cd ui && npm install && npm run build && cd ..
./bin/benchmark-ui --reports-dir=./benchmark-reports --port=8080
```

## Tools

| Binary | Source | Purpose |
|---|---|---|
| `kube-inflater` | `cmd/kube-resource-inflater/` | Bulk-create any resource type — ConfigMaps, Secrets, Pods, Jobs, StatefulSets, CRDs, Services, Namespaces, ServiceAccounts, and hollow kubemark nodes |
| `watch-agent` | `cmd/watch-agent/` | Open thousands of concurrent watch connections (`watch`) or generate mutations (`mutate`); measures event throughput, delivery latency, and reconnect behavior |
| `benchmark-ui` | `cmd/benchmark-ui/` + `ui/` | Web dashboard (Go server + React SPA) to launch runs, monitor them live, view interactive charts, and export PDFs |
| `perf-report` | `cmd/perf-report/` | Auto-discover all API endpoints and benchmark GET/LIST latency; outputs markdown or JSON |
| `cleanup-nodes` | `cmd/cleanup-nodes/` | Surgically remove zombie nodes, NotReady nodes, and problematic pods with concurrent deletion |
| `analyze-notready` | `cmd/analyze-notready/` | Correlate NotReady hollow nodes with their physical hosts; identify per-node health |
| `discover-endpoints` | `cmd/discover-endpoints/` | List all API server resource endpoints with verb/scope information |
| `etcd-tester` | `etcd-tester/` | Direct etcd connectivity and write-throughput testing (separate Go module) |

---

## kube-inflater — Resource Inflater

A single binary for all resource inflation. Select what to create with `--resource-types`.

**Supported types (10):** `configmaps` · `customresources` · `hollownodes` · `jobs` · `namespaces` · `pods` · `secrets` · `serviceaccounts` · `services` · `statefulsets`

> **Pod-bearing types are KWOK-only.** When `pods`, `jobs`, or `statefulsets` are selected, KWOK fake nodes are automatically provisioned. All pods schedule on KWOK nodes with `type=kwok` node selector and tolerations — no real kubelet load.

### Examples

```bash
# 1000 ConfigMaps and Secrets
./bin/kube-inflater --resource-types=configmaps,secrets --count=1000

# 100k ConfigMaps at high throughput
./bin/kube-inflater --resource-types=configmaps --count=100000 --workers=100 --qps=200

# 500k pods with aggressive batching across 100 namespaces
./bin/kube-inflater --resource-types=pods --count=500000 \
  --workers=200 --qps=500 --burst=1000 \
  --batch-initial=100 --batch-factor=2 --max-batches=30 \
  --spread-namespaces=100 --batch-pause=1

# Hollow kubemark nodes (DaemonSet-based, 5 containers per pod)
./bin/kube-inflater --resource-types=hollownodes --count=100
./bin/kube-inflater --resource-types=hollownodes --count=500 --containers-per-pod=10

# Mix resource types in a single run
./bin/kube-inflater --resource-types=hollownodes,configmaps,pods --count=1000

# Generate benchmark reports (markdown + JSON for the web UI)
./bin/kube-inflater --resource-types=pods --count=5000 \
  --benchmark-report --json-report --report-output-dir=./benchmark-reports

# Preview without creating
./bin/kube-inflater --resource-types=configmaps --count=100 --dry-run

# Cleanup a specific run by ID
./bin/kube-inflater --cleanup-only --run-id=20260406-145106-2239
```

### Flag Reference

<details>
<summary>General flags</summary>

| Flag | Default | Description |
|---|---|---|
| `--resource-types` | `configmaps` | Comma-separated resource types to create |
| `--count` | `100` | Number of resources to create per type |
| `--workers` | `10` | Concurrent worker goroutines |
| `--qps` | `50` | Client-go QPS limit |
| `--burst` | `100` | Client-go burst limit |
| `--batch-initial` | `10` | First batch size |
| `--batch-factor` | `2` | Exponential growth factor between batches |
| `--max-batches` | `10` | Maximum number of batches |
| `--batch-pause` | `0` | Seconds to pause between batches |
| `--spread-namespaces` | `10` | Number of namespaces to distribute resources across |
| `--namespace` | `stress-test` | Base namespace prefix (`stress-test-0` … `stress-test-N`) |
| `--data-size` | varies | Payload size in bytes for ConfigMaps/Secrets (max 1 MB) |
| `--dry-run` | `false` | Log what would be created without touching the cluster |
| `--cleanup-only` | `false` | Delete resources from a previous run then exit |
| `--run-id` | auto | Run ID for labeling and scoped cleanup; auto-generated as `YYYYMMDD-HHMMSS-RAND` |
| `--benchmark-report` | `false` | Run API latency tests after creation and generate a markdown report |
| `--json-report` | `false` | Write a JSON report file for the benchmark UI |
| `--report-output-dir` | `.` | Directory for report files |

</details>

<details>
<summary>KWOK flags</summary>

| Flag | Default | Description |
|---|---|---|
| `--kwok-nodes` | `10` | Number of KWOK fake nodes (auto-scales up for pod count) |
| `--kwok-cleanup-controller` | `false` | Also remove the KWOK controller on cleanup |

KWOK nodes each advertise 32 vCPU, 256 GiB RAM, and 1000 pod slots. The controller image is `registry.k8s.io/kwok/kwok:v0.7.0`. Five KWOK Stages manage node and pod lifecycle: `node-initialize`, `node-heartbeat-with-lease`, `pod-ready`, `pod-complete`, `pod-delete`.

</details>

<details>
<summary>Hollow node flags</summary>

| Flag | Default | Description |
|---|---|---|
| `--containers-per-pod` | `5` | Kubemark containers per DaemonSet pod |
| `--kubemark-image` | `k3sacr1.azurecr.io/kubemark:lease-csi-cache-v2` | Kubemark container image |
| `--hollow-namespace` | `kubemark-incremental-test` | Namespace for the DaemonSet and supporting resources |
| `--hollow-wait-timeout` | `3000` | Seconds to wait for hollow nodes to become Ready |
| `--prune-previous` | `false` | Delete older DaemonSets before creating a new one |
| `--retain-daemonsets` | `1` | Number of recent DaemonSets to keep when pruning |
| `--node-status-frequency` | `60s` | Kubelet node-status update interval |
| `--node-lease-duration` | `240` | Node lease duration in seconds |
| `--node-monitor-grace` | `240s` | Controller-manager grace period |
| `--token-audiences` | K8s defaults | Comma-separated ServiceAccount token audiences |

Hollow nodes are kubemark processes deployed via a DaemonSet. Each DaemonSet pod runs N kubelet+kube-proxy container pairs, so total hollow nodes = `scheduled pods × containers-per-pod`. The tool creates a ServiceAccount, RBAC, kubeconfig Secret (token valid for 30 days), and the DaemonSet itself, then polls until all expected nodes register as Ready.

</details>

---

## watch-agent — Watch Stress Testing

Opens concurrent watch connections against the API server and measures event delivery latency, throughput, and reconnect behavior. Two subcommands: `watch` and `mutate`.

Ships as a container image (`Dockerfile.watch-agent`—distroless, non-root) for in-cluster deployment via Kubernetes Jobs.

### Watch Mode

```bash
# Local standalone
./bin/watch-agent watch --connections=100 --duration=60 --resource-type=customresources

# High-scale with staggered startup and namespace spreading
./bin/watch-agent watch --connections=5000 --duration=300 \
  --stagger=30 --spread-count=10 --namespace=stress-test
```

| Flag | Default | Description |
|---|---|---|
| `--connections` | `1` | Number of concurrent watch connections |
| `--duration` | `60` | Run duration in seconds |
| `--stagger` | `0` | Seconds over which to spread initial watch setup |
| `--resource-type` | `customresources` | Resource type to watch |
| `--namespace` | `stress-test` | Namespace prefix |
| `--spread-count` | `10` | Number of namespaces to watch across |
| `--json-report-dir` | — | Directory for JSON report output |

Watches reconnect automatically with 500–2500 ms exponential backoff. Delivery latency is measured using a `mutated-at` RFC 3339 annotation on each resource, giving end-to-end event propagation time.

### Mutator Mode

Generates create→update→delete cycles on `StressItem` custom resources to produce watch events.

```bash
./bin/watch-agent mutate --rate=100 --duration=300 --batch-size=50
```

| Flag | Default | Description |
|---|---|---|
| `--rate` | `100` | Target mutations per second (0 = unlimited) |
| `--duration` | `60` | Run duration in seconds |
| `--batch-size` | `10` | Items per create/update/delete cycle |
| `--data-size` | `1024` | Payload size in bytes |

### In-Cluster Deployment

Manifests in `deploy/watch-agent/`:

| File | Purpose |
|---|---|
| `rbac.yaml` | ServiceAccount, ClusterRole, RoleBinding in `watch-stress` namespace |
| `flowschema.yaml` | API Priority & Fairness exemption (precedence 100 → `exempt` level) |
| `job.yaml.tmpl` | Job template with `__REPLICAS__`, `__CONNS_PER_POD__`, `__DURATION__`, etc. |
| `mutator-job.yaml.tmpl` | Companion mutator Job template |

The `scripts/watch-ramp-cluster.sh` script automates multi-round ramp-up testing: it iterates through connection levels (e.g. 100 → 500 → 1000 → 5000 → 25000), launches Jobs, waits for completion, aggregates metrics from result ConfigMaps via `jq`, and writes a CSV timeline.

---

## benchmark-ui — Web Dashboard

A Go HTTP server + React SPA for launching benchmark runs, monitoring them live with real-time cluster stats, viewing interactive charts, and exporting PDFs.

```bash
# Build backend + frontend
mage benchmarkUI
cd ui && npm install && npm run build && cd ..

# Start the server
./bin/benchmark-ui --reports-dir=./benchmark-reports --port=8080
```

| Flag | Default | Description |
|---|---|---|
| `--reports-dir` | `./benchmark-reports` | Primary directory for report JSON files |
| `--extra-reports-dir` | `/tmp/benchmark-reports` | Additional report directory |
| `--port` | `8080` | HTTP listen port |
| `--static-dir` | `./ui/dist` | Frontend build directory |
| `--bin-dir` | `./bin` | Directory containing CLI binaries |

### Pages

| Route | Page | Description |
|---|---|---|
| `/` | Dashboard | Lists active runs (polled every 3 s) and completed reports, grouped by type |
| `/new-run` | New Run | Form to start pod-creation, api-latency, or watch-stress runs |
| `/run/:id` | Run Monitor | Live log stream (SSE) + real-time cluster charts (nodes, pods, API health) |
| `/report/pod-creation/:id` | Pod Creation Report | Batch throughput bars, cumulative creation line, failure rate per batch |
| `/report/watch-stress/:id` | Watch Stress Report | Latency breakdown bars, dual-axis scaling chart (events/sec vs connect latency) |
| `/report/api-latency/:id` | API Latency Report | Top-N slowest endpoints, latency histogram, response-size-vs-latency scatter |

### API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/reports?type=...` | List reports filtered by type |
| `GET` | `/api/reports/:id` | Single report JSON |
| `POST` | `/api/runs` | Start a new run (spawns CLI subprocess) |
| `GET` | `/api/runs` | List all runs |
| `GET` | `/api/runs/:id` | Run details + logs |
| `GET` | `/api/runs/events/:id` | SSE stream of log lines and status changes |
| `GET` | `/api/cluster/live/:id` | SSE stream of cluster snapshots via `clustermon.Monitor` |

### Generating Reports

All three CLI tools can write JSON reports that the UI consumes:

```bash
# Pod creation report
./bin/kube-inflater --resource-types=pods --count=5000 \
  --json-report --report-output-dir=./benchmark-reports

# API latency report
./bin/perf-report --json --output-dir=./benchmark-reports

# Watch stress report
./bin/watch-agent watch --connections=100 --duration=60 \
  --json-report-dir=./benchmark-reports
```

Every report page includes a **PDF Export** button (renders the page to canvas via html2canvas, then converts to multi-page PDF with jsPDF).

---

## perf-report — API Latency Reports

Auto-discovers all API server endpoints via the discovery API and benchmarks GET/LIST latency with `?limit=1` to minimize payload. Sorts results by API group priority. Outputs a timestamped markdown report with top-10 slowest endpoints and summary statistics.

```bash
./bin/perf-report                              # Full report (limit=1 per request)
./bin/perf-report --only-common                # Only common endpoints (faster)
./bin/perf-report --no-limits                  # Download full resource lists (caution on large clusters)
./bin/perf-report --json --output-dir /tmp     # Also write JSON for the benchmark UI
```

---

## cleanup-nodes — Node & Pod Cleanup

Targeted cleanup of hollow/kubemark nodes and problematic pods with concurrent deletion, rate-limit controls, and a dry-run preview.

```bash
./bin/cleanup-nodes --dry-run                          # Preview what would be deleted
./bin/cleanup-nodes --zombie-only --force               # Remove nodes without backing pods
./bin/cleanup-nodes --notready-only --force             # Remove NotReady nodes
./bin/cleanup-nodes -l kubemark=true --force            # Delete all nodes matching a label selector
./bin/cleanup-nodes --include-pods --pending-only       # Also clean Pending pods
./bin/cleanup-nodes --include-pods --failed-only        # Also clean Failed pods
./bin/cleanup-nodes --concurrency=50 --delay=5          # Tune deletion speed
```

<details>
<summary>All flags</summary>

| Flag | Default | Description |
|---|---|---|
| `--dry-run` | `false` | Show what would be deleted without acting |
| `--zombie-only` | `false` | Delete only zombie nodes (no corresponding pod) |
| `--notready-only` | `false` | Delete only NotReady nodes |
| `--force` | `false` | Force delete with zero grace period (skip confirmation) |
| `-l`, `--label-selector` | — | Delete all nodes matching this label selector |
| `--include-pods` | `false` | Also analyze and cleanup problematic pods |
| `--pending-only` | `false` | Only delete Pending pods (requires `--include-pods`) |
| `--failed-only` | `false` | Only delete Failed pods (requires `--include-pods`) |
| `--concurrency` | `10` | Concurrent deletions |
| `--delay` | `10ms` | Delay between deletions |
| `--namespace` | `kubemark-incremental-test` | Namespace to check for hollow node pods |
| `--disable-rate-limit` | `false` | Disable client-side rate limiting |

</details>

---

## analyze-notready — NotReady Node Inspector

Groups hollow nodes by the physical host they run on and shows per-host counts of NotReady nodes, non-running pods (Pending/Failed/Unknown), zombie nodes, and running pods. Helps pinpoint which physical machines are unhealthy.

```bash
./bin/analyze-notready                                 # Default summary
./bin/analyze-notready --details                       # Show detailed pod information
./bin/analyze-notready --pod-status                    # Show non-running pod reasons
./bin/analyze-notready --sort=zombie                   # Sort by zombie count
```

Sort options: `notready` (default), `total`, `node`, `nonrunning`, `zombie`.

---

## etcd-tester — etcd Probe

A separate Go module (`etcd-tester/`) for direct etcd connectivity and performance testing. Supports TLS and authentication.

**Connectivity mode** — runs a comprehensive test suite covering connection, KV operations, range/list, watch, lease/TTL, auth, performance (100 PUTs/GETs), and Kubernetes-pattern simulations (pod registry, conditional updates, node registration/heartbeat).

**Performance mode** — simulates N kubelet nodes against etcd with concurrent registration, 10-second heartbeats, periodic status updates, 5% node churn every 30 seconds, and a watch on `registry/nodes/*`.

```bash
mage etcdTester
./bin/etcd-tester localhost:2379                  # Connectivity test suite
./bin/etcd-tester perf localhost:2379 1000 5      # 1000 simulated nodes, 5-minute run
./bin/etcd-tester perf localhost:2379 5000 0      # 5000 nodes, run indefinitely
```

---

## Core Concepts

### Run ID

Every run generates a unique ID (`YYYYMMDD-HHMMSS-RAND`) stamped as a `run-id` label on every created resource. This enables scoped cleanup — pass `--cleanup-only --run-id=<id>` to delete exactly one run's resources without touching anything else.

### Exponential Batching

Resources are created in exponentially growing batches to avoid shocking the API server at startup. `CalculateBatchesPlan(initial=10, factor=2, total=1000)` produces batches of 10, 20, 40, 80, 160, 320, 320 (last batch capped to hit the total). A configurable pause between batches allows the control plane to stabilize.

### Namespace Spreading

Namespaced resources are distributed across `<namespace>-0` through `<namespace>-N` using `index % SpreadNamespaces`. This reduces per-namespace etcd contention and distributes LIST/WATCH load. Default: 10 namespaces with prefix `stress-test`.

### KWOK Integration

[KWOK](https://kwok.sigs.k8s.io/) (Kubernetes Without Kubelet) provides fake nodes that accept pod scheduling without running actual containers. The inflater auto-deploys the KWOK controller, Stage CRDs (for node/pod lifecycle simulation), and fake nodes. Each KWOK node advertises 1000 pod slots, so `NodesNeeded = ceil(totalPods / 1000)`.

### Hollow Nodes (Kubemark)

Kubemark creates lightweight kubelet processes that register as real nodes. The inflater deploys them via a DaemonSet — each pod runs multiple kubemark container pairs (kubelet + kube-proxy). Hollow nodes appear as real `Ready` nodes to the control plane, including lease renewals and status updates, putting realistic load on the API server and etcd.

### Custom Resource (StressItem)

The `stressitems.stresstest.kube-inflater.io/v1alpha1` CRD is used by both `kube-inflater --resource-types=customresources` and `watch-agent` for watch stress testing. Schema: `spec.payload` (string) + `spec.index` (int64). Namespaced.

### Standard Labels

Every generated resource carries three labels for identification and cleanup:
```yaml
app: kube-resource-inflater
run-id: <runID>
resource-type: <typeName>
```

---

## Project Layout

```
cmd/
  kube-resource-inflater/   Unified inflater CLI (builds as kube-inflater)
  watch-agent/              Watch stress agent (watch + mutate subcommands)
  benchmark-ui/             Web UI server (Go HTTP + SPA)
  perf-report/              API latency report generator
  cleanup-nodes/            Node/pod cleanup utility
  analyze-notready/         NotReady node analyzer
  discover-endpoints/       API endpoint lister
internal/
  config/                   Shared configuration types and defaults
  inflater/                 Resource creation engine + cleanup with worker pools
  resourcegen/              Per-type generators (10 types) + generator registry
  plan/                     Exponential batch-size planning
  kwok/                     KWOK controller, Stage CRDs, and fake-node provisioning
  daemonsetspec/            DaemonSet spec builder for kubemark pods
  nodes/                    Node listing and filtering by labels / run-id
  perfv2/                   Endpoint discovery and latency reporter
  watchstress/              Watch connection manager and resource mutator
  benchmarkio/              Report types, JSON/markdown I/O, report listing
  clustermon/               Live cluster state snapshot monitor
deploy/
  watch-agent/              Kubernetes manifests (Job, RBAC, FlowSchema)
etcd-tester/                Standalone etcd test module (separate go.mod)
scripts/
  watch-ramp-cluster.sh     Multi-round watch ramp-up orchestrator
  upgrade-control-plane.sh  SSH-based multi-node K8s component upgrade
ui/                         React + Vite + Tailwind frontend for benchmark-ui
  src/pages/                Dashboard, NewRun, RunMonitor, report pages
  src/components/charts/    Chart.js visualizations (10 chart types)
```

## Build Targets (Mage)

```bash
mage -l                    # List all targets
mage build                 # Build kube-inflater (unified binary) → bin/kube-inflater
mage cleanupNodes          # Build cleanup-nodes → bin/cleanup-nodes
mage benchmarkUI           # Build benchmark-ui server → bin/benchmark-ui
mage frontendBuild         # Build React frontend (npm run build in ui/)
mage etcdTester            # Build etcd-tester → bin/etcd-tester (separate module)
mage perfReport            # Build & run perf-report (latency mode, limit=1)
mage perfReportFull        # Build & run perf-report (full data, no limits)
mage analyzeNotReady       # Build & run NotReady analyzer
mage test                  # Run all unit tests (go test ./...)
mage clean                 # Remove bin/
```

Or build directly with Go:

```bash
go build -o bin/kube-inflater ./cmd/kube-resource-inflater
go build -o bin/watch-agent ./cmd/watch-agent
go build -o bin/benchmark-ui ./cmd/benchmark-ui
go build -o bin/perf-report ./cmd/perf-report
go build -o bin/cleanup-nodes ./cmd/cleanup-nodes
cd etcd-tester && go build -o ../bin/etcd-tester
```

## Requirements

- **Go** 1.23+
- **Node.js** 18+ and npm (for the benchmark UI frontend)
- **Kubernetes** 1.20+ with RBAC
- **kubeconfig** with cluster-admin or equivalent permissions
- **Network** access to `k3sacr1.azurecr.io` (kubemark images, anonymous pull) and `registry.k8s.io` (KWOK controller)

## Debugging

```bash
# Hollow nodes
kubectl get daemonsets -n kubemark-incremental-test
kubectl get pods -n kubemark-incremental-test -o wide
kubectl get nodes -l kubemark=true --no-headers | wc -l
kubectl describe pod -n kubemark-incremental-test <pod>

# KWOK
kubectl get deployment kwok-controller -n kube-system
kubectl get nodes -l type=kwok --no-headers | wc -l
kubectl get stages -A

# Watch agent
kubectl get jobs -n watch-stress
kubectl get configmaps -n watch-stress -l app=watch-agent

# Stress-test resources
kubectl get stressitems -A --no-headers | wc -l
kubectl get pods -l app=kube-resource-inflater -A --no-headers | wc -l
```

