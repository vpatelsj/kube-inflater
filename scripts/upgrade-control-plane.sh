#!/bin/bash

set -e

# Function to print status
print_status() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

print_error() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $1" >&2
}

# Check for ssh-agent
if [ -z "$SSH_AUTH_SOCK" ]; then
    print_error "SSH_AUTH_SOCK not set. Please start ssh-agent or add keys to it:"
    print_error "  eval \$(ssh-agent -s)"
    print_error "  ssh-add"
    exit 1
fi

# Create SSH multiplexing socket directory
SSH_SOCKET_DIR="${HOME}/.ssh/multiplex"
mkdir -p "$SSH_SOCKET_DIR"

# SSH options for connection multiplexing and timeouts
SSH_OPTS=(
    "-o" "ConnectTimeout=30"
    "-o" "ServerAliveInterval=60"
    "-o" "ServerAliveCountMax=3"
    "-o" "ControlMaster=auto"
    "-o" "ControlPersist=60"
    "-o" "ControlPath=${SSH_SOCKET_DIR}/%h_%p_%r"
    "-o" "StrictHostKeyChecking=no"
)

# Function to run command with retries
run_with_retry() {
    local max_attempts=3
    local attempt=1

    while true; do
        if "$@"; then
            return 0
        fi

        if [ "$attempt" -ge "$max_attempts" ]; then
            return 1
        fi

        wait_time=$((2 ** (attempt - 1)))
        print_status "  Retry attempt $((attempt + 1))/$max_attempts (waiting ${wait_time}s)..."
        sleep "$wait_time"
        attempt=$((attempt + 1))
    done
}

run_remote() {
    local port="$1"
    shift
    ssh "${SSH_OPTS[@]}" -p "$port" "${USERNAME}@${TARGET_HOST}" "$@"
}

wait_for_local_apiserver() {
    local port="$1"
    local max_attempts="${2:-60}"
    local sleep_seconds="${3:-5}"
    local attempt

    print_status "  Waiting for kube-apiserver to serve requests on port $port..."

    for ((attempt=1; attempt<=max_attempts; attempt++)); do
        if run_remote "$port" "sudo kubectl --kubeconfig=/etc/kubernetes/admin.conf --server=https://127.0.0.1:6443 --insecure-skip-tls-verify get --raw='/readyz' >/dev/null 2>&1"; then
            print_status "  kube-apiserver on port $port is healthy"
            return 0
        fi

        sleep "$sleep_seconds"
    done

    print_error "kube-apiserver on port $port did not become healthy in time"
    return 1
}

wait_for_static_pod() {
    local port="$1"
    local pod_name="$2"
    local max_attempts="${3:-24}"
    local sleep_seconds="${4:-5}"
    local attempt

    print_status "  Waiting for $pod_name on port $port..."

    for ((attempt=1; attempt<=max_attempts; attempt++)); do
        if run_remote "$port" "sudo crictl ps --name '$pod_name' -q 2>/dev/null | grep -q ."; then
            print_status "  $pod_name on port $port is running"
            return 0
        fi

        sleep "$sleep_seconds"
    done

    print_error "$pod_name on port $port did not become ready in time"
    return 1
}

restart_static_pod() {
    local port="$1"
    local manifest_path="$2"
    local pod_name="$3"
    local manifest_name

    manifest_name=$(basename "$manifest_path")

    print_status "  Restarting $pod_name on port $port..."
    run_remote "$port" "set -e; tmp_path=/tmp/${manifest_name}; sudo mv '${manifest_path}' \"\$tmp_path\"; sleep 5; ids=\$(sudo crictl ps -a --name '${pod_name}' -q 2>/dev/null || true); if [ -n \"\$ids\" ]; then echo \"\$ids\" | xargs -r sudo crictl rm -f >/dev/null 2>&1 || true; fi; sudo mv \"\$tmp_path\" '${manifest_path}'"
}

ensure_control_plane_permissions() {
    local port="$1"

    print_status "  Ensuring control-plane file permissions on port $port..."
    run_remote "$port" "set -e; sudo chgrp 65532 /etc/kubernetes/pki; sudo chmod 750 /etc/kubernetes/pki; sudo find /etc/kubernetes/pki -maxdepth 1 -type f -name '*.key' -exec chgrp 65532 {} +; sudo find /etc/kubernetes/pki -maxdepth 1 -type f -name '*.key' -exec chmod 640 {} +; sudo find /etc/kubernetes/pki -maxdepth 1 -type f -name '*.crt' -exec chmod 644 {} +; sudo find /etc/kubernetes/pki -maxdepth 1 -type f -name '*.pub' -exec chgrp 65532 {} +; sudo find /etc/kubernetes/pki -maxdepth 1 -type f -name '*.pub' -exec chmod 644 {} +; sudo chgrp 65532 /etc/kubernetes/admin.conf /etc/kubernetes/controller-manager.conf /etc/kubernetes/kubelet.conf /etc/kubernetes/scheduler.conf; sudo chmod 640 /etc/kubernetes/admin.conf /etc/kubernetes/controller-manager.conf /etc/kubernetes/kubelet.conf /etc/kubernetes/scheduler.conf"
}

shield_apiserver_port() {
    local port="$1"

    print_status "  Temporarily isolating port 6443 on port $port..."
    run_remote "$port" "set -e; node_ip=\$(ip -4 addr show | awk '/inet 10\\.1\\.0\\./ {sub(/\\/.*/, \"\", \$2); print \$2; exit}'); if [ -z \"\$node_ip\" ]; then echo 'failed to determine node IP' >&2; exit 1; fi; sudo iptables -D INPUT -j KUBE_UPGRADE_6443 >/dev/null 2>&1 || true; sudo iptables -F KUBE_UPGRADE_6443 >/dev/null 2>&1 || true; sudo iptables -X KUBE_UPGRADE_6443 >/dev/null 2>&1 || true; sudo iptables -N KUBE_UPGRADE_6443; sudo iptables -A KUBE_UPGRADE_6443 -p tcp --dport 6443 -s 127.0.0.1/32 -j RETURN; sudo iptables -A KUBE_UPGRADE_6443 -p tcp --dport 6443 -s \"\$node_ip\"/32 -j RETURN; sudo iptables -A KUBE_UPGRADE_6443 -p tcp --dport 6443 -j DROP; sudo iptables -I INPUT 1 -j KUBE_UPGRADE_6443"
}

unshield_apiserver_port() {
    local port="$1"

    print_status "  Restoring normal port 6443 traffic on port $port..."
    run_remote "$port" "while sudo iptables -C INPUT -j KUBE_UPGRADE_6443 >/dev/null 2>&1; do sudo iptables -D INPUT -j KUBE_UPGRADE_6443 >/dev/null 2>&1 || true; done; sudo iptables -F KUBE_UPGRADE_6443 >/dev/null 2>&1 || true; sudo iptables -X KUBE_UPGRADE_6443 >/dev/null 2>&1 || true"
}

# Configuration
TARGET_HOST="20.48.184.8"

PORTS=(
    "50000"
    "50001"
    "50002"
    "50003"
    "50004"
    "50007"
)

USERNAME="azureuser"
K8S_MANIFESTS_DIR="/etc/kubernetes/manifests"
K8S_VERSION="1.36.0"

IMAGE_TARS=(
    "/tmp/api.tar"
    "/tmp/cm.tar"
    "/tmp/sched.tar"
)

IMAGE_NAMES=(
    "registry.k8s.io/kube-apiserver:v${K8S_VERSION}"
    "registry.k8s.io/kube-controller-manager:v${K8S_VERSION}"
    "registry.k8s.io/kube-scheduler:v${K8S_VERSION}"
)


# Verify tar files exist
print_status "Verifying tar files exist..."
for tar_file in "${IMAGE_TARS[@]}"; do
    if [ ! -f "$tar_file" ]; then
        print_error "Tar file not found: $tar_file"
        exit 1
    fi
done
print_status "All tar files verified"

# Main loop through ports on the single target host
for i in "${!PORTS[@]}"; do
    host="$TARGET_HOST"
    port="${PORTS[$i]}"
    apiserver_updated=0
    controller_manager_updated=0
    scheduler_updated=0
    apiserver_recovery_mode=0

    print_status "Processing host $host (port $port)"

    # Copy tar files to host
    print_status "Copying tar files to $host..."
    for tar_file in "${IMAGE_TARS[@]}"; do
        filename=$(basename "$tar_file")

        # Check if file already exists on remote
        cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"test -f $tar_file\""
        echo "    [CHECK] $cmd"
        if run_remote "$port" "test -f $tar_file"; then
            print_status "  $filename already exists on $host, skipping"
            continue
        fi

        print_status "  Copying $filename..."
        cmd="scp ${SSH_OPTS[@]} -C -P $port $tar_file ${USERNAME}@${host}:${tar_file}"
        echo "    [CMD] $cmd"
        if run_with_retry scp "${SSH_OPTS[@]}" -C -P "$port" "$tar_file" "${USERNAME}@${host}:${tar_file}"; then
            print_status "  $filename copied successfully"
        else
            print_error "Failed to copy $filename to $host after 3 attempts"
            continue
        fi
    done

    # Import images on host
    print_status "Importing images on $host..."
    for j in "${!IMAGE_TARS[@]}"; do
        tar_file="${IMAGE_TARS[$j]}"
        image_name="${IMAGE_NAMES[$j]}"

        # Check if image already exists at the correct version
        cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo ctr -n k8s.io images ls | grep -qF '$image_name'\""
        echo "    [CHECK] $cmd"
        if run_remote "$port" "sudo ctr -n k8s.io images ls | grep -qF '$image_name'"; then
            print_status "  Image $image_name already imported on $host, skipping"
            continue
        fi

        print_status "  Importing $tar_file as $image_name..."

        cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo ctr -n k8s.io images import $tar_file\""
        echo "    [CMD] $cmd"
        if run_with_retry run_remote "$port" "sudo ctr -n k8s.io images import $tar_file"; then
            print_status "  $image_name imported successfully"
        else
            print_error "Failed to import $tar_file on $host after 3 attempts"
            continue
        fi
    done

    # Update static manifests
    print_status "Updating static manifests on $host..."

    # Update kube-apiserver.yaml
    cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo grep -q 'kube-apiserver:v${K8S_VERSION}' ${K8S_MANIFESTS_DIR}/kube-apiserver.yaml\""
    echo "    [CHECK] $cmd"
    if run_remote "$port" "sudo grep -q 'kube-apiserver:v${K8S_VERSION}' ${K8S_MANIFESTS_DIR}/kube-apiserver.yaml"; then
        print_status "  kube-apiserver.yaml already updated to v${K8S_VERSION}, skipping"
    else
        cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo sed -E -i 's|image:[[:space:]]*[^[:space:]]*/kube-apiserver:v[0-9.]+|image: registry.k8s.io/kube-apiserver:v${K8S_VERSION}|g' ${K8S_MANIFESTS_DIR}/kube-apiserver.yaml\""
        echo "    [CMD] $cmd"
        if run_with_retry run_remote "$port" "sudo sed -E -i 's|image:[[:space:]]*[^[:space:]]*/kube-apiserver:v[0-9.]+|image: registry.k8s.io/kube-apiserver:v${K8S_VERSION}|g' ${K8S_MANIFESTS_DIR}/kube-apiserver.yaml"; then
            print_status "  kube-apiserver.yaml updated successfully"
            apiserver_updated=1
        else
            print_error "Failed to update kube-apiserver.yaml on $host after 3 attempts"
        fi
    fi

    # Update kube-controller-manager.yaml
    cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo grep -q 'kube-controller-manager:v${K8S_VERSION}' ${K8S_MANIFESTS_DIR}/kube-controller-manager.yaml\""
    echo "    [CHECK] $cmd"
    if run_remote "$port" "sudo grep -q 'kube-controller-manager:v${K8S_VERSION}' ${K8S_MANIFESTS_DIR}/kube-controller-manager.yaml"; then
        print_status "  kube-controller-manager.yaml already updated to v${K8S_VERSION}, skipping"
    else
        cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo sed -E -i 's|image:[[:space:]]*[^[:space:]]*/kube-controller-manager:v[0-9.]+|image: registry.k8s.io/kube-controller-manager:v${K8S_VERSION}|g' ${K8S_MANIFESTS_DIR}/kube-controller-manager.yaml\""
        echo "    [CMD] $cmd"
        if run_with_retry run_remote "$port" "sudo sed -E -i 's|image:[[:space:]]*[^[:space:]]*/kube-controller-manager:v[0-9.]+|image: registry.k8s.io/kube-controller-manager:v${K8S_VERSION}|g' ${K8S_MANIFESTS_DIR}/kube-controller-manager.yaml"; then
            print_status "  kube-controller-manager.yaml updated successfully"
            controller_manager_updated=1
        else
            print_error "Failed to update kube-controller-manager.yaml on $host after 3 attempts"
        fi
    fi

    # Update kube-scheduler.yaml
    cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo grep -q 'kube-scheduler:v${K8S_VERSION}' ${K8S_MANIFESTS_DIR}/kube-scheduler.yaml\""
    echo "    [CHECK] $cmd"
    if run_remote "$port" "sudo grep -q 'kube-scheduler:v${K8S_VERSION}' ${K8S_MANIFESTS_DIR}/kube-scheduler.yaml"; then
        print_status "  kube-scheduler.yaml already updated to v${K8S_VERSION}, skipping"
    else
        cmd="ssh ${SSH_OPTS[@]} -p $port ${USERNAME}@${host} \"sudo sed -E -i 's|image:[[:space:]]*[^[:space:]]*/kube-scheduler:v[0-9.]+|image: registry.k8s.io/kube-scheduler:v${K8S_VERSION}|g' ${K8S_MANIFESTS_DIR}/kube-scheduler.yaml\""
        echo "    [CMD] $cmd"
        if run_with_retry run_remote "$port" "sudo sed -E -i 's|image:[[:space:]]*[^[:space:]]*/kube-scheduler:v[0-9.]+|image: registry.k8s.io/kube-scheduler:v${K8S_VERSION}|g' ${K8S_MANIFESTS_DIR}/kube-scheduler.yaml"; then
            print_status "  kube-scheduler.yaml updated successfully"
            scheduler_updated=1
        else
            print_error "Failed to update kube-scheduler.yaml on $host after 3 attempts"
        fi
    fi

    if run_with_retry ensure_control_plane_permissions "$port"; then
        print_status "  Control-plane permissions look correct"
    else
        print_error "Failed to update control-plane permissions on $host after 3 attempts"
        exit 1
    fi

    if [ "$apiserver_updated" -eq 1 ]; then
        apiserver_recovery_mode=1
    elif ! wait_for_local_apiserver "$port" 1 0; then
        apiserver_recovery_mode=1
    fi

    if [ "$apiserver_recovery_mode" -eq 1 ]; then
        if run_with_retry shield_apiserver_port "$port"; then
            print_status "  Port 6443 shielded on port $port"
        else
            print_error "Failed to isolate port 6443 on $host after 3 attempts"
            exit 1
        fi
    fi

    if [ "$apiserver_updated" -eq 0 ] && [ "$apiserver_recovery_mode" -eq 1 ]; then
        if run_with_retry restart_static_pod "$port" "${K8S_MANIFESTS_DIR}/kube-apiserver.yaml" "kube-apiserver"; then
            print_status "  kube-apiserver restarted on port $port"
        else
            unshield_apiserver_port "$port" >/dev/null 2>&1 || true
            print_error "Failed to restart kube-apiserver on $host after 3 attempts"
            exit 1
        fi
    fi

    if ! wait_for_local_apiserver "$port"; then
        if [ "$apiserver_recovery_mode" -eq 1 ]; then
            unshield_apiserver_port "$port" >/dev/null 2>&1 || true
        fi
        exit 1
    fi

    if [ "$apiserver_recovery_mode" -eq 1 ]; then
        if run_with_retry unshield_apiserver_port "$port"; then
            print_status "  Port 6443 restored on port $port"
        else
            print_error "Failed to restore port 6443 on $host after 3 attempts"
            exit 1
        fi
    fi

    if [ "$controller_manager_updated" -eq 0 ] && ! wait_for_static_pod "$port" "kube-controller-manager" 1 0; then
        if run_with_retry restart_static_pod "$port" "${K8S_MANIFESTS_DIR}/kube-controller-manager.yaml" "kube-controller-manager"; then
            print_status "  kube-controller-manager restarted on port $port"
        else
            print_error "Failed to restart kube-controller-manager on $host after 3 attempts"
            exit 1
        fi
    fi

    wait_for_static_pod "$port" "kube-controller-manager"

    if [ "$scheduler_updated" -eq 0 ] && ! wait_for_static_pod "$port" "kube-scheduler" 1 0; then
        if run_with_retry restart_static_pod "$port" "${K8S_MANIFESTS_DIR}/kube-scheduler.yaml" "kube-scheduler"; then
            print_status "  kube-scheduler restarted on port $port"
        else
            print_error "Failed to restart kube-scheduler on $host after 3 attempts"
            exit 1
        fi
    fi

    wait_for_static_pod "$port" "kube-scheduler"

    print_status "  Allowing node $port to settle before continuing..."
    sleep 10

    print_status "Completed processing for $host"
    echo ""
done

print_status "All hosts processed successfully"
