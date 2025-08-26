# kube-inflater 🎈

A **simplified** Kubernetes scalability exerciser that inflates your cluster with hollow (kubemark) nodes using deployments. Just specify how many nodes to add - the tool automatically handles deployment numbering, waits for readiness, and measures API responsiveness.

## Quick Start

### 1. Build
```bash
# Using Mage (recommended)
go install github.com/magefile/mage@latest
mage build

# Or with plain Go
go mod tidy
go build -o bin/kube-inflater ./cmd/kube-inflater
```

### 2. Inflate Your Cluster! 
```bash
# Inflate cluster with 10 hollow nodes
./bin/kube-inflater --nodes-to-add 10

# Inflate cluster with 25 hollow nodes  
./bin/kube-inflater --nodes-to-add 25

# Big inflation! Add 100 hollow nodes for large scale testing
./bin/kube-inflater --nodes-to-add 100
```

### 3. Generate Performance Report (Optional)
```bash
# Generate comprehensive API performance report
./bin/perf-report

# Generate report with only common endpoints (faster)
./bin/perf-report --only-common

# Save report to specific directory
./bin/perf-report --output-dir /tmp/perf-reports
```

### 4. Deflate When Done
```bash
./bin/kube-inflater --cleanup-only
```

## How It Works

The tool automatically:

1. **Finds Next Deployment Number**: Scans existing deployments and creates `hollow-nodes-N` (where N is auto-incremented)
2. **Creates Single Deployment**: One deployment with the specified number of replicas  
3. **Inflates Cluster**: Monitors nodes until they're ready and cluster is fully inflated
4. **Measures Performance**: Tests API response times under inflated load
5. **Reports Results**: Shows timing and node status

Example run:
```
🎈 [INFO] Starting kube-inflater - expanding your cluster capacity!
[INFO] Configuration: NodesToAdd=10, Timeout=50m0s
[PUMP] Will create deployment hollow-nodes-3 with 10 replicas
[PUMP] Creating deployment hollow-nodes-3 with 10 replicas  
[PUMP] Successfully created deployment hollow-nodes-3
[INFLATE] Inflating cluster with 10 hollow nodes...
[INFLATE] Ready nodes: 10/10
🎈 [FULL] Cluster fully inflated! All nodes ready!
[GAUGE] Performance Results:
[GAUGE]   Average per call: 85.30ms
🎈 [INFO] kube-inflater completed successfully! Cluster fully inflated!
```

## Performance Report Generator

The `perf-report` tool generates comprehensive Kubernetes API performance reports by testing various endpoints and measuring response times. This is useful for understanding your cluster's API server performance characteristics.

### Features
- **Automatic Endpoint Discovery**: Scans all available Kubernetes API endpoints
- **Performance Measurement**: Tests response times for GET operations across different resource types
- **Markdown Report**: Generates a detailed report with timing statistics and cluster information
- **Flexible Options**: Test all endpoints or just common ones for faster execution

### Usage Examples
```bash
# Full performance report (tests all discovered endpoints)
./bin/perf-report

# Quick report with only common endpoints (pods, services, nodes, etc.)
./bin/perf-report --only-common

# Save report to custom location
./bin/perf-report --output-dir /tmp/cluster-reports

# Use custom kubeconfig
./bin/perf-report --kubeconfig /path/to/kubeconfig
```

### Generated Report
The tool creates a markdown file named `api-performance-<cluster-name>-<timestamp>.md` containing:
- **Cluster Information**: Version, node count, resource summary
- **Performance Summary**: Statistics for all tested endpoints
- **Detailed Results**: Response times, success rates, and error analysis
- **Recommendations**: Performance insights and optimization suggestions

This is particularly useful for:
- 🔍 **Cluster Health Assessment**: Understanding API server responsiveness
- 📊 **Performance Baseline**: Establishing performance benchmarks
- 🚀 **Scale Testing**: Measuring impact of increased hollow node counts
- 🔧 **Troubleshooting**: Identifying slow or problematic API endpoints

## Configuration Options

| Flag | Description | Default |
|------|-------------|---------|
| `--nodes-to-add` | Number of nodes to add | 10 |
| `--timeout` | Seconds to wait for nodes to become ready | 3000 |
| `--perf-wait` | Seconds to wait before measuring performance | 30 |
| `--perf-tests` | Number of API calls to test for performance | 5 |
| `--skip-perf-tests` | Skip performance tests entirely | false |
| `--cleanup-only` | Only cleanup test resources and exit | false |
| `--node-status-frequency` | Kubelet node status update frequency | "60s" |
| `--node-lease-duration` | Node lease duration (seconds) | 120 |
| `--node-monitor-grace` | Node monitor grace period | "240s" |
| `--token-audiences` | ServiceAccount token audiences | Default K8s audiences |

Environment variables are supported using uppercase names with underscores (e.g., `NODES_TO_ADD`, `TIMEOUT`).

## Container Image

The tool uses the optimized kubemark image from Azure Container Registry:
- **Image**: `k3sacr1.azurecr.io/kubemark:node-heartbeat-optimized-latest`
- **Anonymous Pull**: Enabled (no authentication required)
- **Features**: Heart-beat optimized, distroless base, direct binary execution

## Examples

### Small Test (Development)
```bash
# Quick test with 5 nodes
./bin/kube-inflater --nodes-to-add 5 --perf-wait 10 --perf-tests 3
```

### Medium Scale Test
```bash
# Test with 50 nodes
./bin/kube-inflater --nodes-to-add 50
```

### Large Scale Test
```bash
# Scale test with 200 nodes and extended timeout
./bin/kube-inflater --nodes-to-add 200 --timeout 1800
```

### Skip Performance Testing
```bash
# Add nodes without performance testing (faster)
./bin/kube-inflater --nodes-to-add 30 --skip-perf-tests
```

## Monitoring & Output

The tool provides real-time progress updates:

- **🏗️ Deployment Creation**: Shows which deployment is being created
- **⏳ Node Readiness**: Live count of ready nodes vs. expected
- **📊 Performance Metrics**: API response times and statistics
- **✅ Success Confirmation**: Clear completion status

Example deployment pattern:
```
Run 1: Creates hollow-nodes-1 (10 replicas)
Run 2: Creates hollow-nodes-2 (15 replicas) 
Run 3: Creates hollow-nodes-3 (20 replicas)
...and so on
```

Check your deployments:
```bash
kubectl get deployments -n kubemark-incremental-test
kubectl get pods -n kubemark-incremental-test
kubectl get nodes -l kubemark=true
```

## Cleanup

Always clean up after testing to avoid cluster resource waste:

```bash
# Cleanup all test resources
./bin/kube-inflater --cleanup-only

# Or manual cleanup
kubectl delete namespace kubemark-incremental-test
kubectl delete nodes -l kubemark=true
```

## Troubleshooting

### Common Issues

**Build Errors**: 
- Ensure Go 1.19+ is installed
- Run `go mod tidy` before building
- Use `mage build` for reliable builds

**Pod Startup Issues**: 
- Check cluster has sufficient resources
- Verify image pull from ACR works: `kubectl run test --image=k3sacr1.azurecr.io/kubemark:node-heartbeat-optimized-latest`
- Check pod logs: `kubectl logs -n kubemark-incremental-test <pod-name>`

**Node Registration Issues**:
- Ensure ServiceAccount and ClusterRoleBinding exist (auto-created)
- Check node status: `kubectl describe node <node-name>`
- Verify token audiences are correct for your cluster

### Debugging Commands

```bash
# Check deployments
kubectl get deployments -n kubemark-incremental-test

# Check pod status  
kubectl get pods -n kubemark-incremental-test

# Check hollow nodes
kubectl get nodes -l kubemark=true -o wide

# Check ServiceAccount setup
kubectl get sa,clusterrolebinding -n kubemark-incremental-test

# View pod details
kubectl describe pod -n kubemark-incremental-test <pod-name>
```

## Architecture

The simplified tool consists of focused modules:

- **`cmd/kube-inflater/main.go`**: Main CLI application with simple orchestration
- **`internal/config/types.go`**: Simple configuration with just `NodesToAdd` field
- **`internal/deploymentspec/`**: Kubernetes Deployment specs for hollow nodes  
- **`internal/nodes/`**: Node listing and management via client-go
- **`internal/naming/`**: Node naming utilities
- **`internal/perf/`** & **`internal/perfv2/`**: Performance measurement

### Key Design Principles

- **🎯 Simplicity First**: One parameter (`--nodes-to-add`) does everything
- **🔢 Auto-increment**: Smart deployment numbering without user intervention  
- **📦 Deployment-based**: Uses Kubernetes Deployments for reliability
- **🚀 Anonymous Pull**: No registry authentication required
- **⚡ Direct Execution**: No shell dependencies, pure Go binary execution

## Development

### Running Tests
```bash
go test ./...                    # All tests
go test -cover ./...            # With coverage  
go test ./internal/config       # Specific package
```

### Building
```bash
mage -l                         # List available targets
mage build                      # Build binary
mage test                       # Run tests
mage clean                      # Clean artifacts
```

### Making Changes

1. **Keep it simple**: The goal is minimal complexity
2. **Test thoroughly**: Verify with different node counts
3. **Update docs**: Keep README in sync with changes
4. **Follow Go conventions**: Use `go fmt` and `go vet`

## Requirements

- **Go**: 1.19 or later
- **Kubernetes**: 1.20+ cluster with RBAC enabled  
- **Access**: Valid kubeconfig with cluster-admin or equivalent permissions
- **Resources**: Sufficient cluster capacity for desired node count
- **Network**: Access to `k3sacr1.azurecr.io` (anonymous pull enabled)

