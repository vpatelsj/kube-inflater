# kube-inflater

A Kubernetes scalability and stress-testing toolkit. Inflate clusters with hollow (kubemark) nodes, bulk-create API resources, stress-test the watch subsystem, benchmark API server latency, and probe etcd — all from a single repo.

## Tools

| Binary | Purpose |
|---|---|
| `kube-inflater` | Deploy DaemonSets of kubemark containers to register hollow nodes |
| `kube-resource-inflater` | Bulk-create ConfigMaps, Secrets, Pods, Jobs, StatefulSets, CRDs, etc. with optional KWOK scheduling |
| `watch-agent` | Open thousands of concurrent watch connections and measure event throughput / delivery latency |
| `perf-report` | Auto-discover API endpoints and generate a markdown performance report |
| `cleanup-nodes` | Surgically remove zombie nodes, NotReady nodes, and problematic pods |
| `analyze-notready` | Correlate NotReady hollow nodes with their physical hosts |
| `discover-endpoints` | List all API server resource endpoints with verb support |
| `etcd-tester` | Connectivity and write-performance testing against etcd (separate Go module) |
| `benchmark-ui` | Web UI for viewing benchmark results with interactive charts and PDF export |

## Quick Start

```bash
# Install Mage (recommended build tool)
go install github.com/magefile/mage@latest

# Build everything
mage build              # kube-inflater
mage resourceInflater   # kube-resource-inflater
mage cleanupNodes       # cleanup-nodes
mage etcdTester         # etcd-tester

# Or build individually with plain Go
go build -o bin/kube-inflater ./cmd/kube-inflater
```

## kube-inflater — Hollow Node Inflation

Deploys a DaemonSet to the `kubemark-incremental-test` namespace. Each pod runs N kubemark containers, so total hollow nodes = `scheduled pods × --containers-per-pod`. The tool waits for all nodes to register Ready, then optionally measures API latency.

```bash
# Default: 5 containers per pod on every eligible node
./bin/kube-inflater

# Higher density
./bin/kube-inflater --containers-per-pod 10

# Custom name, skip perf tests
./bin/kube-inflater --daemonset-name my-run --skip-perf-tests

# Cleanup
./bin/kube-inflater --cleanup-only
```

<details>
<summary>All flags</summary>

| Flag | Default | Description |
|---|---|---|
| `--containers-per-pod` | 5 | Kubemark containers per DaemonSet pod |
| `--daemonset-name` | auto | Name for the DaemonSet (timestamp + random suffix) |
| `--timeout` | 3000 | Seconds to wait for nodes to become Ready |
| `--perf-wait` | 30 | Seconds to settle before performance measurement |
| `--perf-tests` | 5 | Number of API calls for latency measurement |
| `--skip-perf-tests` | false | Skip performance measurement entirely |
| `--cleanup-only` | false | Delete test resources and exit |
| `--prune-previous` | false | Delete older DaemonSets before creating a new one |
| `--retain-daemonsets` | 1 | Number of recent DaemonSets to keep when pruning |
| `--node-status-frequency` | 60s | Kubelet node-status update interval |
| `--node-lease-duration` | 240 | Node lease duration in seconds |
| `--node-monitor-grace` | 240s | Controller-manager grace period |
| `--token-audiences` | K8s defaults | Comma-separated SA token audiences |

Env vars supported: `CONTAINERS_PER_POD`, `WAIT_TIMEOUT`, `PERFORMANCE_WAIT`, etc.

</details>

### Large-Scale Inflation

The included `inflate-250k.sh` script incrementally deploys up to 50 DaemonSets (10 containers each) to scale a cluster toward 250k+ hollow nodes. It is reentrant — it detects existing `hollow-step-N` DaemonSets and resumes.

## kube-resource-inflater — Bulk Resource Creation

Creates thousands of Kubernetes resources across spread namespaces using exponential batching and a configurable worker pool.

**Supported resource types:** `configmaps`, `secrets`, `services`, `namespaces`, `pods`, `serviceaccounts`, `jobs`, `statefulsets`, `customresources`

> **Pod benchmarking is KWOK-only**: When `pods`, `jobs`, or `statefulsets` are selected, KWOK fake nodes are automatically provisioned. Pods always schedule on KWOK nodes — no real kubelet load, no `--kwok` flag needed.

```bash
# 1000 ConfigMaps and Secrets (default settings)
./bin/kube-resource-inflater --resource-types=configmaps,secrets --count=1000

# 100k ConfigMaps, high throughput
./bin/kube-resource-inflater --resource-types=configmaps --count=100000 --workers=100 --qps=200

# 500k pods on KWOK fake nodes (KWOK auto-enabled)
./bin/kube-resource-inflater --resource-types=pods --count=500000 \
  --workers=200 --qps=500 --burst=1000 \
  --batch-initial=100 --batch-factor=2 --max-batches=30 \
  --spread-namespaces=100 --batch-pause=1

# Generate a benchmark report (includes creation throughput + API latency)
./bin/kube-resource-inflater --resource-types=pods --count=5000 \
  --benchmark-report --report-output-dir=/tmp/reports

# Preview without creating anything
./bin/kube-resource-inflater --resource-types=configmaps --count=100 --dry-run

# Cleanup a specific run
./bin/kube-resource-inflater --cleanup-only --run-id=<id>
```

## watch-agent — Watch Stress Testing

Opens concurrent watch connections against the API server and measures event delivery latency, throughput, and reconnect behavior. Ships as a container image for in-cluster deployment via Kubernetes Jobs.

```bash
# Build the container image
docker build -f Dockerfile.watch-agent -t watch-agent:latest .

# Local standalone test
./bin/watch-agent watch --connections=100 --duration=60 --resource-type=customresources

# Mutator mode: create/update/delete resources to generate watch events
./bin/watch-agent mutate --rate=100 --duration=300 --batch-size=50
```

Deployment manifests live in `deploy/watch-agent/` — a Job template, RBAC, and a FlowSchema for priority-level exemption. The companion script `scripts/watch-ramp-cluster.sh` automates multi-round ramp-up testing.

## perf-report — API Performance Reports

Auto-discovers all API server endpoints and benchmarks GET latency. Outputs a timestamped markdown report.

```bash
./bin/perf-report                              # Full report
./bin/perf-report --only-common                # Only common resources (faster)
./bin/perf-report --no-limits                  # Download full resource lists (caution on large clusters)
./bin/perf-report --output-dir /tmp/reports    # Custom output directory
```

## cleanup-nodes — Node & Pod Cleanup

Targeted cleanup of hollow/kubemark nodes and pods with concurrent deletion and rate-limit controls.

```bash
./bin/cleanup-nodes --dry-run                          # Preview
./bin/cleanup-nodes --zombie-only --force               # Remove nodes without backing pods
./bin/cleanup-nodes --notready-only --force             # Remove NotReady nodes
./bin/cleanup-nodes -l kubemark=true                    # Delete all nodes matching label
./bin/cleanup-nodes --include-pods --pending-only       # Also clean Pending pods
./bin/cleanup-nodes --concurrency=50 --delay=5          # Tune deletion speed
```

## etcd-tester

A separate Go module (`etcd-tester/`) for direct etcd connectivity and write-throughput testing.

```bash
mage etcdTester
./bin/etcd-tester localhost:2379                  # Connectivity checks
./bin/etcd-tester perf localhost:2379 1000 5      # 1000 simulated nodes, 5-minute run
```

## benchmark-ui — Benchmark Visualization

A local web application for viewing benchmark results with interactive charts and PDF export. Reads JSON report files produced by the CLI tools.

```bash
# Generate JSON reports during benchmark runs
./bin/kube-resource-inflater --resource-types=pods --count=5000 \
  --json-report --report-output-dir=./benchmark-reports

./bin/perf-report --json --output-dir=./benchmark-reports

./bin/watch-agent watch --connections=100 --duration=60 \
  --json-report-dir=./benchmark-reports

# Build and run the UI
mage benchmarkUI
cd ui && npm install && npm run build && cd ..
./bin/benchmark-ui --reports-dir=./benchmark-reports --port=8080
```

**Report types and charts:**
- **Pod Creation** — batch throughput bars, cumulative creation line, failure rate per batch
- **Watch Stress** — latency breakdown, connection scaling (dual-axis events/sec + connect latency)
- **API Latency** — top-N slowest endpoints, response-size-vs-latency scatter, latency histogram, full results table

Each report page includes a PDF export button.

## Project Layout

```
cmd/
  kube-inflater/          Hollow node inflation CLI
  kube-resource-inflater/ Bulk resource creation CLI
  watch-agent/            Watch stress agent (watch + mutate subcommands)
  perf-report/            API latency report generator
  cleanup-nodes/          Node/pod cleanup utility
  analyze-notready/       NotReady node analyzer
  discover-endpoints/     API endpoint lister
internal/
  config/                 Shared configuration types and defaults
  inflater/               Resource creation engine with exponential batching
  resourcegen/            Per-type resource generators (ConfigMap, Secret, Pod, CRD, …)
  plan/                   Batch-size planning (exponential growth)
  kwok/                   KWOK controller provisioning and fake-node management
  daemonsetspec/          DaemonSet spec builder for kubemark pods
  deploymentspec/         Deployment spec builder
  podspec/                Pod spec helpers
  nodes/                  Node listing and filtering
  naming/                 Hollow-node naming utilities
  perf/                   Legacy performance stats
  perfv2/                 Endpoint discovery and performance reporter
  watchstress/            Watch connection manager and resource mutator
deploy/
  watch-agent/            Kubernetes manifests (Job, RBAC, FlowSchema)
etcd-tester/              Standalone etcd test module
scripts/                  Operational shell scripts
```

## Build Targets (Mage)

```bash
mage -l                    # List all targets
mage build                 # Build kube-inflater
mage resourceInflater      # Build kube-resource-inflater
mage cleanupNodes          # Build cleanup-nodes
mage etcdTester            # Build etcd-tester
mage benchmarkUI           # Build benchmark-ui server
mage frontendBuild         # Build frontend (npm run build in ui/)
mage perfReport            # Build & run perf-report (latency mode)
mage perfReportFull        # Build & run perf-report (full data mode)
mage analyzeNotReady       # Build & run NotReady analyzer
mage test                  # Run all unit tests
mage clean                 # Remove bin/
```

## Requirements

- **Go** 1.23+
- **Kubernetes** 1.20+ with RBAC
- **kubeconfig** with cluster-admin or equivalent permissions
- **Network** access to `k3sacr1.azurecr.io` for kubemark images (anonymous pull)

## Debugging

```bash
kubectl get daemonsets -n kubemark-incremental-test
kubectl get pods -n kubemark-incremental-test
kubectl get nodes -l kubemark=true -o wide
kubectl describe pod -n kubemark-incremental-test <pod>
```

