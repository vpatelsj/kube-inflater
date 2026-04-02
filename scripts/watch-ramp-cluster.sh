#!/usr/bin/env bash
# watch-ramp-cluster.sh — Ramp up concurrent watches from pods INSIDE the cluster.
# Each pod runs watch-agent with M connections. Total watches = replicas × conns_per_pod.
#
# Prerequisites:
#   1. CRs already in cluster (from a previous run)
#   2. watch-agent image built and pushed to a registry accessible by the cluster
#   3. RBAC applied: kubectl apply -f deploy/watch-agent/rbac.yaml
#
# Usage:
#   IMAGE=myregistry/watch-agent:latest bash scripts/watch-ramp-cluster.sh
set -euo pipefail

IMAGE="${IMAGE:?Set IMAGE=<registry>/watch-agent:<tag>}"
DURATION="${DURATION:-60}"
PAUSE="${PAUSE:-15}"
STAGGER="${STAGGER:-0}"
CONNS_PER_POD="${CONNS_PER_POD:-1}"
MAX_PODS="${MAX_PODS:-100000}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-10}"
JOB_TIMEOUT="${JOB_TIMEOUT:-600}"
MUTATION_RATE="${MUTATION_RATE:-100}"
DATA_SIZE="${DATA_SIZE:-1024}"
RESOURCE_TYPE="${RESOURCE_TYPE:-customresources}"
SPREAD_COUNT="${SPREAD_COUNT:-10}"
STRESS_NAMESPACE="${STRESS_NAMESPACE:-stress-test}"
NAMESPACE="watch-stress"

# Watch connection counts to test — total watches per round
# Override with space-separated string: LEVELS="100 500 1000"
if [[ -n "${LEVELS:-}" ]]; then
    read -ra LEVEL_ARRAY <<< "$LEVELS"
else
    LEVEL_ARRAY=(100 500 1000 2000 3000 5000 7500 10000 15000 20000 25000)
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TEMPLATE="$SCRIPT_DIR/deploy/watch-agent/job.yaml.tmpl"
MUTATOR_TEMPLATE="$SCRIPT_DIR/deploy/watch-agent/mutator-job.yaml.tmpl"

log()  { echo -e "[$(date +%H:%M:%S)] $*"; }
pass() { echo -e "${GREEN}$*${NC}"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }

check_apiserver() {
    local start end ms
    start=$(date +%s%N)
    if kubectl get --raw /healthz --request-timeout="${HEALTH_TIMEOUT}s" &>/dev/null; then
        end=$(date +%s%N)
        ms=$(( (end - start) / 1000000 ))
        echo "$ms"
        return 0
    fi
    return 1
}

cleanup_job() {
    # Delete both jobs (cascade deletes their owned pods)
    kubectl delete job watch-agent -n "$NAMESPACE" --ignore-not-found --timeout=30s &>/dev/null || true
    kubectl delete job watch-mutator -n "$NAMESPACE" --ignore-not-found --timeout=30s &>/dev/null || true
    # Only wait for pods if we know which run to filter for
    [[ -z "${RUN_ID:-}" ]] && return 0
    local deadline=$(( $(date +%s) + 30 ))
    while [[ $(date +%s) -lt "$deadline" ]]; do
        count=$(kubectl get pods -n "$NAMESPACE" -l "run-id=$RUN_ID" --no-headers 2>/dev/null | wc -l)
        [[ "$count" -eq 0 ]] && return 0
        kubectl delete pods -n "$NAMESPACE" -l "run-id=$RUN_ID" --force --grace-period=0 &>/dev/null || true
        sleep 3
    done
    warn "Some pods from run $RUN_ID still present after cleanup"
}

wait_for_job() {
    local timeout=$1
    local start=$(date +%s)
    while true; do
        local now=$(date +%s)
        local elapsed=$(( now - start ))
        [[ "$elapsed" -ge "$timeout" ]] && return 1

        # If job was already TTL-cleaned, check if pods finished
        if ! kubectl get job watch-agent -n "$NAMESPACE" &>/dev/null; then
            return 0
        fi

        # Check if job succeeded or failed
        local status
        status=$(kubectl get job watch-agent -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}' 2>/dev/null)
        [[ "$status" == "True" ]] && return 0

        status=$(kubectl get job watch-agent -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Failed")].status}' 2>/dev/null)
        [[ "$status" == "True" ]] && return 0

        # Also check if all pods are done (Succeeded or Failed)
        local total=$(kubectl get job watch-agent -n "$NAMESPACE" -o jsonpath='{.spec.completions}' 2>/dev/null)
        local succeeded=$(kubectl get job watch-agent -n "$NAMESPACE" -o jsonpath='{.status.succeeded}' 2>/dev/null)
        local failed=$(kubectl get job watch-agent -n "$NAMESPACE" -o jsonpath='{.status.failed}' 2>/dev/null)
        local active=$(kubectl get job watch-agent -n "$NAMESPACE" -o jsonpath='{.status.active}' 2>/dev/null)
        local done_count=$(( ${succeeded:-0} + ${failed:-0} ))
        [[ "$done_count" -ge "${total:-999999}" ]] && return 0

        # Progress output
        local remaining=$(( timeout - elapsed ))
        local cm_count=$(kubectl get configmaps -n "$NAMESPACE" -l "app=watch-agent,run-id=$RUN_ID" --no-headers 2>/dev/null | wc -l)
        printf "\r  [%3ds/%ds] Pods: %s running, %s succeeded, %s failed | ConfigMaps collected: %s/%s | Timeout in: %ds  " \
            "$elapsed" "$timeout" "${active:-0}" "${succeeded:-0}" "${failed:-0}" "$cm_count" "${total:-?}" "$remaining"

        sleep 5
    done
}

collect_metrics() {
    local total_events=0 total_reconn=0 total_errors=0
    local sum_avg_connect=0 max_connect=0 pod_count=0
    local sum_peak_alive=0
    local sum_avg_delivery=0 max_delivery=0 max_p99_delivery=0

    # Collect results from ConfigMaps pushed by watch-agent pods
    local cm_json
    cm_json=$(kubectl get configmaps -n "$NAMESPACE" -l "app=watch-agent,run-id=$RUN_ID" -o json 2>/dev/null) || true

    local cm_count
    cm_count=$(echo "$cm_json" | jq '.items | length' 2>/dev/null) || cm_count=0

    for (( i=0; i<cm_count; i++ )); do
        local line
        line=$(echo "$cm_json" | jq -r ".items[$i].data[\"result.json\"]" 2>/dev/null) || continue
        [[ -z "$line" || "$line" == "null" ]] && continue

        events=$(echo "$line" | jq -r '.events // 0')
        reconn=$(echo "$line" | jq -r '.reconnects // 0')
        errs=$(echo "$line" | jq -r '.errors // 0')
        avg_c=$(echo "$line" | jq -r '.avg_connect_ms // 0')
        max_c=$(echo "$line" | jq -r '.max_connect_ms // 0')
        peak_a=$(echo "$line" | jq -r '.peak_alive_watches // 0')
        avg_d=$(echo "$line" | jq -r '.avg_delivery_ms // 0')
        max_d=$(echo "$line" | jq -r '.max_delivery_ms // 0')
        p99_d=$(echo "$line" | jq -r '.p99_delivery_ms // 0')

        total_events=$((total_events + events))
        total_reconn=$((total_reconn + reconn))
        total_errors=$((total_errors + errs))
        sum_avg_connect=$(echo "$sum_avg_connect + $avg_c" | bc)
        max_connect=$(echo "$max_connect $max_c" | tr ' ' '\n' | sort -g | tail -1)
        sum_peak_alive=$((sum_peak_alive + peak_a))
        sum_avg_delivery=$(echo "$sum_avg_delivery + $avg_d" | bc)
        max_delivery=$(echo "$max_delivery $max_d" | tr ' ' '\n' | sort -g | tail -1)
        max_p99_delivery=$(echo "$max_p99_delivery $p99_d" | tr ' ' '\n' | sort -g | tail -1)
        pod_count=$((pod_count + 1))
    done

    if [[ "$pod_count" -eq 0 ]]; then
        echo "0 0 0 0 0 0 0 0 0 0 0"
        return
    fi

    local avg_connect
    avg_connect=$(echo "scale=3; $sum_avg_connect / $pod_count" | bc)
    local avg_delivery
    avg_delivery=$(echo "scale=3; $sum_avg_delivery / $pod_count" | bc)
    local eps
    eps=$(echo "scale=1; $total_events / $DURATION" | bc)

    echo "$total_events $eps $total_reconn $total_errors $avg_connect $max_connect $sum_peak_alive $pod_count $avg_delivery $max_delivery $max_p99_delivery"
}

launch_mutator() {
    # Generate and apply the mutator job manifest
    # Mutator duration is slightly longer than watch duration to ensure events flow the entire time
    local mutator_duration=$(( DURATION + 15 ))
    sed -e "s|__IMAGE__|$IMAGE|g" \
        -e "s|__RATE__|$MUTATION_RATE|g" \
        -e "s|__DURATION__|$mutator_duration|g" \
        -e "s|__DATA_SIZE__|$DATA_SIZE|g" \
        -e "s|__RUN_ID__|$RUN_ID|g" \
        "$MUTATOR_TEMPLATE" | kubectl apply -f - &>/dev/null

    # Wait for mutator pod to be Running
    local deadline=$(( $(date +%s) + 60 ))
    while [[ $(date +%s) -lt "$deadline" ]]; do
        local phase
        phase=$(kubectl get pods -n "$NAMESPACE" -l app=watch-mutator,run-id="$RUN_ID" -o jsonpath='{.items[0].status.phase}' 2>/dev/null)
        [[ "$phase" == "Running" ]] && return 0
        sleep 2
    done
    warn "Mutator pod did not reach Running state within 60s"
    return 1
}

collect_mutator_metrics() {
    local pod
    pod=$(kubectl get pods -n "$NAMESPACE" -l app=watch-mutator,run-id="$RUN_ID" -o name --no-headers 2>/dev/null | head -1)
    [[ -z "$pod" ]] && { echo "0 0 0 0 0"; return; }

    local line
    line=$(kubectl logs -n "$NAMESPACE" "$pod" --tail=5 2>/dev/null | grep '^{' | tail -1)
    [[ -z "$line" ]] && { echo "0 0 0 0 0"; return; }

    local creates updates deletes errs rate
    creates=$(echo "$line" | jq -r '.creates // 0')
    updates=$(echo "$line" | jq -r '.updates // 0')
    deletes=$(echo "$line" | jq -r '.deletes // 0')
    errs=$(echo "$line" | jq -r '.errors // 0')
    rate=$(echo "$line" | jq -r '.actual_rate // 0')
    echo "$creates $updates $deletes $errs $rate"
}

# --- Main ---

RESULTS_FILE="/tmp/watch-ramp-cluster-$(date +%Y%m%d-%H%M%S).csv"
echo "watches,pods,conns_per_pod,events,events_per_sec,reconnects,errors,peak_alive,avg_connect_ms,max_connect_ms,avg_delivery_ms,max_delivery_ms,p99_delivery_ms,mut_creates,mut_updates,mut_deletes,mut_errors,mut_rate,api_health_ms,status" > "$RESULTS_FILE"

log "Cluster Watch Ramp-Up Stress Test"
log "Image: $IMAGE"
log "Connections per pod: $CONNS_PER_POD (max pods: $MAX_PODS)"
log "Mutation rate: $MUTATION_RATE/s, data size: ${DATA_SIZE}B"
log "Resource type: $RESOURCE_TYPE, spread: $SPREAD_COUNT, namespace: $STRESS_NAMESPACE"
log "Levels: ${LEVEL_ARRAY[*]}"
log "Duration per round: ${DURATION}s, Stagger: ${STAGGER}s, Pause: ${PAUSE}s"
log "Results: $RESULTS_FILE"

# Pre-flight: ensure all prerequisites are in place
log "Running pre-flight checks..."

# 1. Apply RBAC (idempotent)
log "Applying RBAC..."
kubectl apply -f "$SCRIPT_DIR/deploy/watch-agent/rbac.yaml"

# 2. Ensure spread namespaces exist
log "Ensuring spread namespaces (${STRESS_NAMESPACE}-0 through ${STRESS_NAMESPACE}-$((SPREAD_COUNT-1)))..."
for i in $(seq 0 $((SPREAD_COUNT - 1))); do
    kubectl create ns "${STRESS_NAMESPACE}-${i}" --dry-run=client -o yaml | kubectl apply -f - 2>/dev/null
done

# 3. Ensure StressItem CRD exists (needed even in zero-mutation mode for watches)
if ! kubectl get crd stressitems.stresstest.kube-inflater.io &>/dev/null; then
    log "Creating StressItem CRD..."
    kubectl apply -f - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: stressitems.stresstest.kube-inflater.io
spec:
  group: stresstest.kube-inflater.io
  names:
    kind: StressItem
    listKind: StressItemList
    plural: stressitems
    singular: stressitem
  scope: Namespaced
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                payload:
                  type: string
                index:
                  type: integer
EOF
    sleep 2
fi

api_ms=$(check_apiserver) || { fail "API server not healthy!"; exit 1; }
log "API server baseline: ${api_ms}ms"

cr_count=$(kubectl get stressitems -A --no-headers --chunk-size=0 2>/dev/null | wc -l)
log "Existing CRs: $cr_count"

printf "\n%-10s %-8s %-12s %-14s %-12s %-12s %-12s %-18s %-15s %-18s %-18s %-18s %-14s\n" \
    "WATCHES" "PODS" "EVENTS" "EVENTS/SEC" "RECONNECTS" "ERRORS" "PEAK_ALIVE" "AVG_CONNECT_MS" "MAX_CONNECT_MS" "AVG_DELIVERY_MS" "MAX_DELIVERY_MS" "P99_DELIVERY_MS" "API_HEALTH_MS"
printf "%-10s %-8s %-12s %-14s %-12s %-12s %-12s %-18s %-15s %-18s %-18s %-18s %-14s\n" \
    "-------" "----" "------" "----------" "----------" "------" "----------" "--------------" "--------------" "----------------" "----------------" "----------------" "-------------"

for TOTAL_WATCHES in "${LEVEL_ARRAY[@]}"; do
    # Calculate replicas and connections per pod, capping at MAX_PODS
    REPLICAS=$(( (TOTAL_WATCHES + CONNS_PER_POD - 1) / CONNS_PER_POD ))
    ACTUAL_CONNS=$CONNS_PER_POD
    if [[ "$REPLICAS" -gt "$MAX_PODS" ]]; then
        REPLICAS=$MAX_PODS
        ACTUAL_CONNS=$(( (TOTAL_WATCHES + REPLICAS - 1) / REPLICAS ))
    fi
    ACTUAL_WATCHES=$(( REPLICAS * ACTUAL_CONNS ))

    # Health check
    api_ms=$(check_apiserver) || { fail "API server unhealthy — stopping."; break; }

    # Clean up any previous job
    cleanup_job

    # Unique label for this round so collect_metrics ignores orphaned pods
    RUN_ID="run-$(date +%s)"

    log "Starting round: $ACTUAL_WATCHES watches ($REPLICAS pods × $ACTUAL_CONNS conns, run=$RUN_ID)..."
    log "  Each pod opens $ACTUAL_CONNS watch connections, staggered over ${STAGGER}s"
    log "  Watches target: $RESOURCE_TYPE in ${STRESS_NAMESPACE}-{0..$((SPREAD_COUNT-1))} (${SPREAD_COUNT} namespaces)"
    log "  Duration: ${DURATION}s per pod, then pods emit JSON metrics to ConfigMap"

    # Launch mutator first so events are flowing before watches connect (skip if rate=0)
    if [[ "$MUTATION_RATE" -gt 0 ]]; then
        log "  Launching mutator ($MUTATION_RATE mutations/sec)..."
        if ! launch_mutator; then
            warn "Failed to start mutator, proceeding anyway..."
        fi
        sleep 3  # give mutator a head start
    else
        log "  Mutation rate is 0 — skipping mutator (idle watch + reconnect mode)"
    fi

    # Generate and apply the watch-agent job manifest
    log "  Creating Job: $REPLICAS pods × $ACTUAL_CONNS conns = $ACTUAL_WATCHES watches..."
    sed -e "s|__IMAGE__|$IMAGE|g" \
        -e "s|__REPLICAS__|$REPLICAS|g" \
        -e "s|__CONNS_PER_POD__|$ACTUAL_CONNS|g" \
        -e "s|__DURATION__|$DURATION|g" \
        -e "s|__STAGGER__|$STAGGER|g" \
        -e "s|__RUN_ID__|$RUN_ID|g" \
        -e "s|__RESOURCE_TYPE__|$RESOURCE_TYPE|g" \
        -e "s|__STRESS_NAMESPACE__|$STRESS_NAMESPACE|g" \
        -e "s|__SPREAD_COUNT__|$SPREAD_COUNT|g" \
        "$TEMPLATE" | kubectl apply -f - &>/dev/null

    log "  Waiting for pods to schedule and start watching..."

    # Wait for job completion (or all pods to finish)
    if ! wait_for_job "$JOB_TIMEOUT"; then
        echo ""  # newline after progress output
        warn "Job did not complete within ${JOB_TIMEOUT}s"
    else
        echo ""  # newline after progress output
        log "  All pods completed."
    fi

    # Small delay for logs to flush
    sleep 3

    # Collect metrics from ConfigMaps
    log "  Collecting metrics from ConfigMaps (run-id=$RUN_ID)..."
    read -r events eps reconn errors avg_c max_c peak_alive pod_count avg_d max_d p99_d <<< "$(collect_metrics)"
    log "  Collected results from $pod_count/$REPLICAS pods"

    # Collect mutator metrics (skip if rate=0)
    if [[ "$MUTATION_RATE" -gt 0 ]]; then
        read -r mut_creates mut_updates mut_deletes mut_errors mut_rate <<< "$(collect_mutator_metrics)"
    else
        mut_creates=0 mut_updates=0 mut_deletes=0 mut_errors=0 mut_rate=0
    fi

    # Post-round health check
    log "  Checking API server health post-round..."
    post_api_ms=$(check_apiserver 2>/dev/null) || post_api_ms="TIMEOUT"

    # Determine status
    status="OK"
    if [[ "$post_api_ms" == "TIMEOUT" ]]; then
        status="API_TIMEOUT"
    elif [[ "$post_api_ms" -gt 5000 ]] 2>/dev/null; then
        status="DEGRADED"
    fi

    log "  Round summary: events=$events eps=$eps reconnects=$reconn errors=$errors peak_alive=$peak_alive api_health=${post_api_ms}ms status=$status"

    printf "%-10s %-8s %-12s %-14s %-12s %-12s %-12s %-18s %-15s %-18s %-18s %-18s %-14s %s\n" \
        "$ACTUAL_WATCHES" "$pod_count/$REPLICAS" "$events" "$eps" "$reconn" "$errors" "$peak_alive" "${avg_c}ms" "${max_c}ms" "${avg_d}ms" "${max_d}ms" "${p99_d}ms" "${post_api_ms}ms" "$status"

    echo "$ACTUAL_WATCHES,$pod_count,$ACTUAL_CONNS,$events,$eps,$reconn,$errors,$peak_alive,$avg_c,$max_c,$avg_d,$max_d,$p99_d,$mut_creates,$mut_updates,$mut_deletes,$mut_errors,$mut_rate,$post_api_ms,$status" >> "$RESULTS_FILE"

    # Clean up result ConfigMaps from this round
    log "  Cleaning up ConfigMaps and Job for this round..."
    kubectl delete configmaps -n "$NAMESPACE" -l "run-id=$RUN_ID" --ignore-not-found &>/dev/null || true

    # Cleanup before next round
    cleanup_job

    if [[ "$status" == "API_TIMEOUT" ]]; then
        fail "API server timed out — stopping ramp."
        break
    fi

    log "Pausing ${PAUSE}s before next round..."
    sleep "$PAUSE"
done

log "Done! Results saved to $RESULTS_FILE"
cat "$RESULTS_FILE"
