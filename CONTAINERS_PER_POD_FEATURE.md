# Containers Per Pod Feature

## Summary

The kube-inflater has been enhanced to support running multiple kubemark containers per pod. This feature allows you to increase node density and resource efficiency by packing multiple hollow nodes into a single pod.

## What Was Changed

### 1. Configuration
- Added `ContainersPerPod` field to the Config struct
- Default value: 5 containers per pod
- Added `DefaultContainersPerPod` constant in config/types.go

### 2. Command Line Interface
- Added `--containers-per-pod` flag to both `kube-inflater` and `kube-pressure-cooker` commands
- Environment variable support: `CONTAINERS_PER_POD`
- Flag description: "Number of kubemark containers (nodes) per pod"

### 3. Deployment Logic
- Modified `MakeHollowDeploymentSpec()` function to create multiple container pairs per pod
- Each container gets a unique suffix (e.g., `-0`, `-1`, `-2`, etc.)
- Each kubemark instance gets unique node names and log files
- Container pairs: `hollow-kubelet-N` and `hollow-proxy-N` for each index N

### 4. Replica Calculation
- Modified replica calculation logic to account for multiple containers per pod
- Formula: `replicasNeeded = (nodesToAdd + containersPerPod - 1) / containersPerPod` (round up division)
- Actual nodes created: `replicasNeeded * containersPerPod`

### 5. Resource Management
- Each kubelet container: 20m CPU, 50Mi memory (requests) / 100m CPU, 200Mi memory (limits)
- Each proxy container: 10m CPU, 25Mi memory (requests) / 50m CPU, 100Mi memory (limits)
- Total per pod with 5 containers: 150m CPU, 375Mi memory (requests) / 750m CPU, 1500Mi memory (limits)

## Usage Examples

### Default (5 containers per pod)
```bash
./bin/kube-inflater --nodes-to-add 10
# Creates: 2 pods × 5 containers each = 10 nodes
```

### Custom containers per pod
```bash
./bin/kube-inflater --nodes-to-add 20 --containers-per-pod 3
# Creates: 7 pods × 3 containers each = 21 nodes (rounds up)

./bin/kube-inflater --nodes-to-add 50 --containers-per-pod 10
# Creates: 5 pods × 10 containers each = 50 nodes
```

### Environment variable
```bash
CONTAINERS_PER_POD=2 ./bin/kube-inflater --nodes-to-add 8
# Creates: 4 pods × 2 containers each = 8 nodes
```

## Benefits

1. **Higher Node Density**: Pack more hollow nodes into fewer pods
2. **Resource Efficiency**: Reduce pod overhead by sharing volumes and network between containers
3. **Flexibility**: Adjust containers per pod based on cluster constraints and testing needs
4. **Backward Compatibility**: Default behavior maintains 5 containers per pod

## Technical Details

### Container Naming Convention
- Kubelet containers: `hollow-kubelet-0`, `hollow-kubelet-1`, ..., `hollow-kubelet-N`
- Proxy containers: `hollow-proxy-0`, `hollow-proxy-1`, ..., `hollow-proxy-N`

### Node Naming Convention
- Node names: `$(POD_NAME)-0`, `$(POD_NAME)-1`, ..., `$(POD_NAME)-N`
- Example: `hollow-nodes-1-abc123-0`, `hollow-nodes-1-abc123-1`, etc.

### Port Configuration
Each kubelet container gets unique ports to avoid conflicts within the same pod:
- Kubelet container 0: `--kubelet-port=10250`, `--kubelet-read-only-port=10255`
- Kubelet container 1: `--kubelet-port=10252`, `--kubelet-read-only-port=10257`
- Kubelet container 2: `--kubelet-port=10254`, `--kubelet-read-only-port=10259`
- And so on (increments by 2 for each container)

### Log Files
- Kubelet logs: `/var/log/kubelet-$(POD_NAME)-0.log`, `/var/log/kubelet-$(POD_NAME)-1.log`, etc.
- Proxy logs: `/var/log/kubeproxy-$(POD_NAME)-0.log`, `/var/log/kubeproxy-$(POD_NAME)-1.log`, etc.

### Labels
Each container gets additional labels:
- `container-index=N` (where N is the container index within the pod)
- Standard labels: `kubemark=true`, `incremental-test=true`, `deployment=deployment-X`

## Files Modified

1. `internal/config/types.go` - Added ContainersPerPod field and default
2. `internal/deploymentspec/deploymentspec.go` - Modified to create multiple containers per pod
3. `cmd/kube-inflater/main.go` - Added flag parsing and replica calculation logic
4. `cmd/kube-pressure-cooker/main.go` - Added flag parsing and replica calculation logic
5. `README.md` - Updated documentation with new flag and examples
