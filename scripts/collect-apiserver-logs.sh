#!/bin/bash
set -euo pipefail

HOST="k3a-canadacentral-vapa-200k-39.canadacentral.cloudapp.azure.com"
OUTDIR="/home/vapa/dev/kube-inflater/apiserver-logs-$(date +%Y%m%d-%H%M%S)"
SSH="ssh -T -o ConnectTimeout=30 -o StrictHostKeyChecking=no -o LogLevel=ERROR -o ControlPath=none"

mkdir -p "$OUTDIR"
echo "Output dir: $OUTDIR"

declare -A PORT_MAP=(
  [50000]=cp-000002
  [50001]=cp-000001
  [50002]=cp-000003
  [50004]=cp-000006
  [50005]=cp-000007
  [50006]=cp-000008
)

for port in "${!PORT_MAP[@]}"; do
  node="${PORT_MAP[$port]}"
  echo -n "Collecting from $node (port $port)... "
  $SSH -p "$port" "azureuser@$HOST" \
    "sudo crictl logs --tail=10000 \$(sudo crictl ps --name kube-apiserver -q) 2>&1" \
    > "$OUTDIR/${node}.log" 2>&1
  lines=$(wc -l < "$OUTDIR/${node}.log")
  size=$(du -h "$OUTDIR/${node}.log" | cut -f1)
  echo "done ($lines lines, $size)"
done

echo ""
echo "All logs collected in $OUTDIR"
ls -lh "$OUTDIR/"
