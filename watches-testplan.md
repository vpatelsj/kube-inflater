

## Test Scenarios

All scenarios use `watch-ramp-cluster.sh` unless noted. **Stop criteria** for all: if `API_HEALTH_MS` exceeds 5000ms or returns `TIMEOUT`, the script stops automatically.

**Control plane metrics to monitor during all scenarios:**
- `apiserver_watch_events_sizes` — memory per event delivery
- `apiserver_current_inflight_requests` — saturation
- `etcd_mvcc_db_total_size_in_bytes` — etcd pressure
- `process_resident_memory_bytes{job="apiserver"}` — API server RSS (expect ~1–2 KB per watch connection)
- API server pod CPU — reconnect processing overhead

---

### Scenario A: Cold-start thundering herd

**What it measures:** Can the API server handle N watches opening simultaneously? Simulates an API server restart or mass node reboot where all clients reconnect at once.

**How it works:** Each round launches N watches via a Job, runs for a short duration, tears them all down, then starts the next level from zero. Watches do NOT carry over between rounds.

**Duration:** Short (120–180s) — just long enough for all watches to establish and observe the initial LIST replay.

```bash
IMAGE=k3sacr1.azurecr.io/watch-agent:latest \
MUTATION_RATE=0 \
CONNS_PER_POD=15 \
DURATION=180 \
STAGGER=30 \
PAUSE=30 \
JOB_TIMEOUT=300 \
LEVELS="1000 5000 15000 50000 100000" \
bash scripts/watch-ramp-cluster.sh
```

**Key metrics:** avg/max connect latency, errors during establishment, API server health under burst.

---

### Scenario B: Steady-state reconnect churn

**What it measures:** Can the API server sustain N concurrent watches under natural reconnect cycling? The API server kills each watch after 5–10 min, so at N watches you get ~N/450 reconnects/sec in steady state.

**How it works:** Same Job-based approach, but each round runs long enough (600s+) that every watch is killed and reconnected at least once by the API server. Watches still do NOT carry over — each level is independent.

**Duration:** Long (600s) — must exceed the API server's 5–10 min watch timeout.

| Watches | Expected reconnects/sec (avg lifetime 450s) |
|---|---|
| 1,000 | ~2 |
| 50,000 | ~111 |
| 100,000 | ~222 |
| 500,000 | ~1,111 |
| 1,500,000 | ~3,333 |

```bash
IMAGE=k3sacr1.azurecr.io/watch-agent:latest \
MUTATION_RATE=0 \
CONNS_PER_POD=15 \
DURATION=600 \
STAGGER=60 \
PAUSE=120 \
JOB_TIMEOUT=900 \
LEVELS="1000 5000 15000 50000 100000 250000 500000 1000000 1500000" \
bash scripts/watch-ramp-cluster.sh
```

**Key metrics:** reconnects/sec at steady state, reconnect latency (not initial connect), API server memory growth, errors from 410 Gone (etcd compaction).

---

### Scenario C: Cumulative ramp (realistic cluster growth)

**What it measures:** What happens as watches accumulate over time, like a real cluster where nodes are added gradually and watches persist? Previous watches stay alive while new ones are added.

**How it works:** Uses a **Deployment** instead of a Job. The script scales the Deployment up between rounds (e.g., 67 → 334 → 1000 replicas). Existing pods keep their watches open. Only new pods go through cold-start.

> **STATUS: Not yet implemented.** Requires changes to watch-agent (run indefinitely instead of exiting after `--duration`) and a new Deployment template + ramp script (`watch-ramp-cumulative.sh`).

```bash
# Planned — not yet implemented
IMAGE=k3sacr1.azurecr.io/watch-agent:latest \
MUTATION_RATE=0 \
CONNS_PER_POD=15 \
MEASURE_WINDOW=120 \
STAGGER=30 \
PAUSE=60 \
LEVELS="1000 5000 15000 50000 100000" \
bash scripts/watch-ramp-cumulative.sh
```

**Key metrics:** incremental connect latency (only new watches), total peak alive, API server response to gradual load vs burst, memory growth curve.

---

### Scenario D: Mutation stress (event fan-out)

**What it measures:** At a fixed watch count, how much event throughput (mutations) can the API server sustain before fan-out saturates?

**How it works:** Hold watches constant at the highest stable level from Scenarios A/B. Ramp mutation rate across separate runs.

At 500k watches with MUTATION_RATE=100, each mutation cycle (create+update+delete = 3 events × 10 batch) generates 30 events/sec, each fanned out to ~50k watchers per namespace (500k / 10 spread) = **1.5M event deliveries/sec**.

```bash
# Run each rate as a separate invocation at the stable watch count
for RATE in 10 50 100 500; do
  IMAGE=k3sacr1.azurecr.io/watch-agent:latest \
  MUTATION_RATE=$RATE \
  CONNS_PER_POD=15 \
  DURATION=600 \
  STAGGER=60 \
  PAUSE=120 \
  LEVELS="500000" \
  bash scripts/watch-ramp-cluster.sh
done
```

**Key metrics:** avg/p99 delivery latency, mutator actual_rate vs target rate, API server CPU saturation, max_connect_ms (reconnect quality under write load).

---

### Scenario E: Sustained soak

**What it measures:** Long-duration stability at the target watch count + mutation rate. Looking for slow leaks and cascading failures.

```bash
IMAGE=k3sacr1.azurecr.io/watch-agent:latest \
MUTATION_RATE=<stable-rate-from-scenario-D> \
CONNS_PER_POD=15 \
DURATION=3600 \
STAGGER=120 \
PAUSE=0 \
JOB_TIMEOUT=4000 \
LEVELS="<stable-watch-count>" \
bash scripts/watch-ramp-cluster.sh
```

**Key metrics:** API server RSS over time (memory leaks), etcd compaction 410 Gone errors, reconnect storm cascades (API server pod restart causing all watches to reconnect at once), leader election stability if running HA API server.

---

### Implementation status

| Scenario | Status | Tool |
|---|---|---|
| A: Cold-start thundering herd | Implemented | `watch-ramp-cluster.sh` (short duration) |
| B: Steady-state reconnect churn | Implemented | `watch-ramp-cluster.sh` (long duration) |
| C: Cumulative ramp | **Not implemented** | Needs Deployment mode + new script |
| D: Mutation stress | Implemented | `watch-ramp-cluster.sh` (MUTATION_RATE>0) |
| E: Sustained soak | Implemented | `watch-ramp-cluster.sh` (DURATION=3600) |

---

## Node Provisioning Plan

You don't need all 173 nodes from the start. Add node pools as you ramp (applies to Scenarios A/B/C):

| Watch level | Pods (15 conns/pod) | Nodes needed (D16s_v3, 25m CPU, 64Mi mem, 580 pods/node) |
|---|---|---|
| 1,000 | 67 | 1 |
| 5,000 | 334 | 1 |
| 15,000 | 1,000 | 2 |
| 50,000 | 3,334 | 6 |
| 100,000 | 6,667 | 12 |
| 250,000 | 16,667 | 29 |
| 500,000 | 33,334 | 58 |
| 1,000,000 | 66,667 | 115 |
| 1,500,000 | 100,000 | 173 |

Scale down after each test session to control cost.

---

## Estimated Wall Time

| Scenario | Rounds | Per round | Approx time |
|---|---|---|---|
| A: Cold-start | 5 | 180s + 30s = 3.5 min | ~18 min |
| B: Steady-state | 9 | 600s + 120s = 12 min | ~1.8 hours |
| C: Cumulative | 5 | ~180s + 60s = 4 min | ~20 min |
| D: Mutation stress | 4 | 600s + 120s = 12 min | ~48 min |
| E: Sustained soak | 1 | 3600s | 1 hour |
| **Total** | | | **~4.5 hours** |

Plus node provisioning time between rounds.

---

## Smoke Test Results (2026-04-01)

### Cluster configuration

- **Control plane:** 6 nodes, 96 vCPUs / 384 GiB each (NoSchedule)
- **Agent nodes:** 10 × D16s_v3 (16 vCPUs / 64 GiB), max-pods=1000
- **Existing CRs:** 99,950 StressItems spread across 10 namespaces (~10k per namespace)
- **Image:** `k3sacr1.azurecr.io/watch-agent:latest`
- **Settings:** `DURATION=120, STAGGER=15, MUTATION_RATE=0, CONNS_PER_POD=15`

### Results

| Watches | Pods OK | Events | Events/sec | Reconnects | Errors | Peak Alive | Avg Connect (ms) | Max Connect (ms) | API Health (ms) | Status |
|---|---|---|---|---|---|---|---|---|---|---|
| 1,005 | 67/67 | 10,045,109 | 83,709 | 1,005 | 0 | 1,005 | 442 | 5,089 | 252 | OK |
| 5,010 | 334/334 | 48,989,130 | 408,243 | 4,635 | 0 | 5,010 | 27,908 | 86,657 | 321 | OK |
| 15,000 | 886/1000 | 61,184,811 | 509,873 | 4,663 | 5,065 | 8,225 | 55,479 | 119,795 | 219 | OK |

### Key findings

1. **Connect latency is the dominant cost.** Average watch connect time scaled from 0.4s → 28s → 55s. This is because each watch open triggers a full LIST of ~10k existing CRs per namespace before streaming begins. At 15k watches the connect time alone consumed nearly the entire 120s duration.

2. **Existing CRs drive event volume, not mutations.** With `MUTATION_RATE=0`, the ~10M events at 1,005 watches came entirely from the "replay" — each watch receives ADDED events for all existing objects in its namespace on connect. Delivery latency was 0ms because the code only measures latency for objects created after the test starts.

3. **Round 3 partial failure.** 114 of 1,000 pods failed to push ConfigMaps (886/1000 collected), 1 pod failed outright, and 5,065 watch errors occurred. The 120s duration was too short for all watches to establish given the 55s avg connect time + 15s stagger. Peak alive only reached 8,225 out of 15,000 target.

4. **Reconnects ≈ watch count.** At 120s duration with 28–55s connect times, most watches only completed one connection cycle. The API server's 5–10 min timeout never kicked in — reconnects were from LIST timeouts or watch failures during the initial burst.

5. **API server stayed healthy.** Health checks remained 219–321ms throughout, even at 15k concurrent watches with 509k events/sec throughput. The 6-node control plane (96 cores each) has significant headroom.

### Implications for full-scale test

- **Duration must be >> connect time.** At higher watch counts connect time will grow further. Use `DURATION=600` minimum (Phase 1 plan) to ensure all watches establish and reach steady state.
- **Stagger should scale with watch count.** 15s stagger was insufficient at 15k watches. Use `STAGGER=60` or higher to avoid thundering herd on watch establishment.
- **Existing CR count matters.** The 99,950 CRs cause significant LIST overhead on every watch connect/reconnect. For a clean idle-watches test, consider running with zero existing CRs. For a realistic test, keep them — real kubelets also LIST existing pods on reconnect.
- **ConfigMap collection scales.** All 67 and 334 ConfigMaps were collected successfully in one API call. At 886/1000 the loss was from pods that didn't finish in time, not from collection failure.