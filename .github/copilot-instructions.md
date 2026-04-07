# Copilot Instructions

## Build, Test, and Lint

This repo uses [Mage](https://magefile.org/) as the build tool (`magefile.go`).

```bash
# Build all binaries
mage build              # kube-inflater (unified binary) → bin/kube-inflater
mage cleanupNodes       # cleanup-nodes
mage etcdTester         # etcd-tester (separate module in etcd-tester/)
mage benchmarkUI        # benchmark-ui → bin/benchmark-ui

# Build frontend (React + Vite)
cd ui && npm install && npm run build
# Or: mage frontendBuild

# Run all tests
mage test
# Or directly:
go test ./...

# Run a single test
go test ./internal/plan/... -run TestCalculateBatchesPlan
go test ./internal/resourcegen/... -run TestGenerators

# Clean
mage clean
```

`etcd-tester/` is a **separate Go module** — build it via `mage etcdTester`, not from the root.

## Architecture

The repo is a Kubernetes scalability/stress-testing toolkit providing several independent CLI binaries under `cmd/`. They share library code under `internal/`.

**Core flow for `kube-inflater` (unified binary, source in `cmd/kube-resource-inflater/`):**
1. `cmd/kube-resource-inflater/main.go` parses flags into `config.ResourceInflaterConfig`
2. `inflater.Engine` orchestrates creation: for batch-based types it calls `plan.CalculateBatchesPlan` for exponential batch sizes, then runs each batch through a worker pool. For setup-based types (e.g. `hollownodes`), it calls the generator's `Setup()` and `WaitForReady()` methods instead.
3. Each batch calls `resourcegen.ResourceGenerator.Generate()` per item, then creates via the dynamic Kubernetes client
4. For pod-bearing types (`pods`, `jobs`, `statefulsets`), KWOK fake nodes are auto-provisioned first via `kwok.Provisioner`
5. For `hollownodes`, a DaemonSet is deployed to register hollow kubemark nodes

**`watch-agent`** runs in-cluster as a Job (manifests in `deploy/watch-agent/`). It has two subcommands:
- `watch`: opens concurrent watch connections and measures throughput/latency via `watchstress.Watcher`
- `mutate`: generates watch events at a configured rate via `watchstress.Mutator`

## Key Conventions

### ResourceGenerator interface (`internal/resourcegen/types.go`)
All resource generators implement this interface:
```go
type ResourceGenerator interface {
    Generate(runID string, namespace string, index int) (*unstructured.Unstructured, error)
    GVR() schema.GroupVersionResource
    IsNamespaced() bool
    Kind() string
    TypeName() string
}
```

Setup-based generators (like `hollownodes`) additionally implement `SetupTeardownGenerator` with `Setup()`, `WaitForReady()`, `Teardown()`, and `IsSetupBased()` methods.

Register new generators in `internal/resourcegen/registry.go`. The registry maps short type names (e.g. `"configmaps"`, `"hollownodes"`) to constructors.

### Standard labels on every generated resource
```go
"app":           "kube-resource-inflater"
"run-id":        <runID>
"resource-type": <typeName>
```
The `run-id` label is critical for scoped cleanup — always include it on any new resource type.

### RunID
A random hex string generated at startup, used as a label and as a namespace/name suffix. All resources from one run share the same `run-id`. Cleanup targets resources by this label.

### Namespace spreading
Namespaced resources are distributed across `<namespace>-0` … `<namespace>-N` using `index % SpreadNamespaces`. Default namespace prefix is `stress-test`, default spread count is 10.

### Exponential batching (`internal/plan/`)
`CalculateBatchesPlan(initial, factor, total, maxBatches)` returns `[][2]int` pairs of `[batchNumber, batchSize]`. Default: initial=10, factor=2 → batches of 10, 20, 40, 80…

### KWOK auto-scaling
When `HasPodBearingTypes()` is true, `Engine.EnsureKWOK` auto-scales `KWOKNodes` upward if the default is insufficient for the requested pod count. Formula: `kwok.NodesNeeded(totalPods)`.

### Kubemark image
`k3sacr1.azurecr.io/kubemark:lease-csi-cache-v2` — anonymous pull from Azure ACR.

### Watch-agent deployment
The `deploy/watch-agent/` manifests include a `FlowSchema` to exempt watch-agent traffic from API priority-and-fairness limits. Always deploy this FlowSchema when running large-scale watch tests.
