#!/bin/bash
#
# Inflate hollow nodes to 250k in steps of ~25 daemonset deployments.
# Each daemonset runs on all worker nodes with 10 containers per pod.
# Reentrant: automatically detects existing hollow-step-N daemonsets and
# resumes from the next step.
#

set -euo pipefail

NAMESPACE="kubemark-incremental-test"
TARGET=300000
STEP_END=50
CONTAINERS_PER_POD=10
INFLATER_DIR="/home/vapa/dev/kube-inflater-ci"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
fail() { echo -e "${RED}[FAIL]${NC} $1" >&2; exit 1; }

get_node_count() {
  kubectl get nodes --no-headers --chunk-size=0 2>/dev/null | wc -l
}

# Detect highest existing hollow-step-N daemonset to resume from
get_last_completed_step() {
  kubectl get ds -n "$NAMESPACE" --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null \
    | { grep -oP '^hollow-step-\K[0-9]+' || true; } \
    | sort -n \
    | tail -1
}

cd "$INFLATER_DIR"

CURRENT=$(get_node_count)
LAST_STEP=$(get_last_completed_step)
STEP_START=$(( ${LAST_STEP:-0} + 1 ))

log "Target: ${TARGET} nodes"
log "Current node count: ${CURRENT}"
if [[ -n "${LAST_STEP:-}" ]]; then
  log "Found existing daemonsets up to hollow-step-${LAST_STEP}, resuming from step ${STEP_START}"
else
  log "No existing hollow-step daemonsets found, starting from step 1"
fi
log "Steps: ${STEP_START} to ${STEP_END} (containers-per-pod=${CONTAINERS_PER_POD})"

if [[ "$CURRENT" -ge "$TARGET" ]]; then
  log "Already at or above target (${CURRENT} >= ${TARGET}). Nothing to do."
  exit 0
fi

if [[ "$STEP_START" -gt "$STEP_END" ]]; then
  warn "All ${STEP_END} steps already deployed. Current nodes: ${CURRENT}"
  exit 0
fi

for step in $(seq "$STEP_START" "$STEP_END"); do
  DS_NAME="hollow-step-${step}"
  echo ""
  echo -e "${CYAN}═══════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Step ${step}/${STEP_END} — daemonset: ${DS_NAME}${NC}"
  echo -e "${CYAN}═══════════════════════════════════════════════════${NC}"

  BEFORE=$(get_node_count)
  log "Nodes before: ${BEFORE}"

  ./bin/kube-inflater \
    --resource-types=hollownodes \
    --count=0 \
    --containers-per-pod "$CONTAINERS_PER_POD" \
    --node-lease-duration 240 \
    --node-status-frequency 60s \
    --node-monitor-grace 240s 2>&1

  # Wait for new hollow nodes to register
  log "Waiting for hollow nodes from ${DS_NAME} to register..."
  for i in $(seq 1 60); do
    AFTER=$(get_node_count)
    if [[ "$AFTER" -gt "$BEFORE" ]]; then
      break
    fi
    sleep 10
  done

  AFTER=$(get_node_count)
  ADDED=$((AFTER - BEFORE))
  log "Nodes after: ${AFTER} (+${ADDED})"

  if [[ "$AFTER" -ge "$TARGET" ]]; then
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  TARGET REACHED: ${AFTER} nodes (target: ${TARGET})${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
    exit 0
  fi

  REMAINING=$((TARGET - AFTER))
  log "Remaining: ${REMAINING} nodes to go"
done

FINAL=$(get_node_count)
warn "Completed all ${STEP_END} steps. Final node count: ${FINAL} (target was ${TARGET})"
