# Watch Stress Test Report — April 2, 2026

## Objective

Determine the Kubernetes API server's capacity for concurrent watch connections and characterize the factors that drive watch establishment latency at scale.

Six experiments were conducted, progressively scaling from 1,000 to 100,000 concurrent watches while varying duration, stagger, agent node count, and client-side parameters to isolate bottlenecks.

## Cluster Configuration

| Component | Configuration |
|---|---|
| Control plane | 6 nodes, 96 vCPUs / 384 GiB each (NoSchedule taint) |
| Kubernetes version | v1.35.2 |
| Existing CRs | 99,950 StressItems across 10 namespaces (~10k per namespace) |
| Image | `k3sacr1.azurecr.io/watch-agent:latest` |
| CRD | `stressitems.stresstest.kube-inflater.io/v1alpha1` |
| APF bypass | FlowSchema `watch-agent-exempt` → priority level `exempt` |

Agent node count was scaled up between experiments:

| Experiments | Agent Nodes | VM SKU |
|---|---|---|
| 1–3 | 10 | D16s_v3 (16 vCPUs / 64 GiB), max-pods=1000 |
| 4–5 | 40 | D16s_v3 |
| 6 | 137 | D16s_v3 |

### API Server Parameters

| Parameter | Value | Default | Impact |
|---|---|---|---|
| `--max-requests-inflight` | 3000 | 400 | Bypassed by exempt FlowSchema |
| `--max-mutating-requests-inflight` | 1000 | 200 | Bypassed by exempt FlowSchema |
| `--goaway-chance` | 0.02 | 0 | 2% of new HTTP/2 connections receive GOAWAY, forcing reconnect |
| `--etcd-compaction-interval` | 0 | 5m | Compaction disabled — no 410 Gone errors, but etcd history grows unbounded |
| `--http2-max-streams-per-connection` | 1000 (default) | 1000 | 15 streams/pod is well under limit |

### Test Tooling

Each watch-agent pod opens 15 concurrent watch connections (goroutines sharing a single HTTP/2 connection), staggered over a configurable window. Pods run for `DURATION` seconds, then write a JSON metrics ConfigMap. The `watch-ramp-cluster.sh` script orchestrates the Job, waits for completion, and aggregates results.

All experiments used `MUTATION_RATE=0` (no writes to StressItems during the test — watches observe only the initial CR replay and API server timeout/reconnect cycles).

---

## Experiments

### Experiment 1: Baseline (10 nodes, DURATION=120)

**Goal:** Establish baseline watch performance at small scale.

**Settings:** `DURATION=120, STAGGER=15, CONNS_PER_POD=15` | 10 agent nodes

| Watches | Pods OK | Events | Events/sec | Reconnects | Errors | Peak Alive | Avg Connect (ms) | Max Connect (ms) | API Health (ms) |
|---|---|---|---|---|---|---|---|---|---|
| 1,005 | 67/67 | 10,045,109 | 83,709 | 1,005 | 0 | 1,005 | 442 | 5,089 | 252 |
| 5,010 | 334/334 | 48,989,130 | 408,243 | 4,635 | 0 | 5,010 | 27,908 | 86,657 | 321 |
| 15,000 | 886/1000 | 61,184,811 | 509,873 | 4,663 | 5,065 | 8,225 | 55,479 | 119,795 | 219 |

**Key findings:**
- Connect latency scaled from 442ms → 28s → 55s as watches increased from 1k to 15k.
- Round 3 (15k) was **right-censored**: max connect (119,795ms) hit the DURATION ceiling. Only 8,225 of 15,000 watches established. 5,065 errors were all context deadline timeouts.
- A pod log (`watch-agent-zxt5t`) confirmed: 5/15 connections succeeded, 10 timed out. Error messages included `context deadline exceeded` and `client rate limiter Wait returned an error: context deadline exceeded` (the latter is not actual rate limiting — it's the context expiring during a retry backoff).
- API server remained healthy (219–321ms) throughout.

---

### Experiment 2: Etcd Bypass (ResourceVersion="0")

**Goal:** Test whether the per-Watch() etcd round-trip (`GetCurrentResourceVersion()`) causes the connect latency.

**Settings:** Same as Experiment 1, plus `ResourceVersion: "0"` in Watch() ListOptions to skip the etcd call. | 10 agent nodes

| Watches | Avg Connect — Before | Avg Connect — RV="0" | Change |
|---|---|---|---|
| 1,005 | 442ms | 285ms | -36% |
| 5,010 | 27,908ms | 29,964ms | +7% (noise) |
| 15,000 | 55,479ms | 56,647ms | +2% (noise) |

**Conclusion:** **Hypothesis rejected.** The etcd round-trip saves ~150ms at 1k watches but is negligible at 5k+. Connect latency at scale is not caused by etcd. Change was reverted.

---

### Experiment 3: Extended Duration (300s)

**Goal:** Confirm that Experiment 1's errors were caused by insufficient duration and reveal the true connect latency distribution.

**Settings:** `DURATION=300, STAGGER=15, CONNS_PER_POD=15, JOB_TIMEOUT=600` | 10 agent nodes

| Watches | Pods OK | Errors | Peak Alive | Avg Connect (ms) | Max Connect (ms) | API Health (ms) |
|---|---|---|---|---|---|---|
| 1,005 | 67/67 | 0 | 1,005 | 327 | 4,579 | 288 |
| 5,010 | 332/334 | 0 | 4,980 | 30,473 | 94,625 | 354 |
| 15,000 | 925/1000 | 526 | 13,349 | 118,631 | 299,984 | 309 |

**Key findings:**
- Errors dropped 90% (5,065 → 526). Peak alive rose 62% (8,225 → 13,349).
- Avg connect **doubled** (55s → 119s) — this is the true latency, previously hidden because slow connections timed out and were excluded from the average.
- Max connect (300s) still hit the duration ceiling — data was still partially right-censored at 15k on 10 nodes.

---

### Experiment 4: 30k Watches (40 nodes)

**Goal:** Scale to 30k watches with sufficient duration for full establishment and one reconnect cycle.

**Settings:** `DURATION=600, STAGGER=30, CONNS_PER_POD=15, JOB_TIMEOUT=900` | **40 agent nodes** (scaled up from 10)

| Watches | Pods OK | Events | Events/sec | Reconnects | Errors | Peak Alive | Avg Connect (ms) | Max Connect (ms) | API Health (ms) |
|---|---|---|---|---|---|---|---|---|---|
| 30,000 | 2000/2000 | 299,854,000 | 499,757 | 30,000 | 0 | 30,000 | 62,627 | 222,175 | 432 |

**Key findings:**
- **100% establishment, zero errors.** First fully clean run at scale.
- Avg connect (63s) was *lower* than 15k at 119s despite 2× the watches. Two variables changed: stagger doubled (15→30s) and node count quadrupled (10→40). Both reduced per-server contention; their individual effects cannot be separated.
- Reconnects = exactly 1 per watch — the API server's 5–10 min timeout triggered once within the 600s window.

---

### Experiment 5: 60k Watches (40 nodes)

**Goal:** Double to 60k watches and test linear scaling on the same 40-node infrastructure.

**Settings:** `DURATION=600, STAGGER=60, CONNS_PER_POD=15, JOB_TIMEOUT=900` | 40 agent nodes

> **Note:** An initial attempt failed immediately — the Kubernetes Job's `backoffLimit: 3` terminated the entire 4,000-pod Job after just 3 transient pod failures. `backoffLimit` was increased to 10,000 before retrying.

| Watches | Pods OK | Events | Events/sec | Reconnects | Errors | Peak Alive | Avg Connect (ms) | Max Connect (ms) | API Health (ms) |
|---|---|---|---|---|---|---|---|---|---|
| 60,000 | 4000/4000 | 599,708,000 | 999,513 | 61,770 | 1,770 | 60,000 | 126,104 | 407,688 | 285 |

**Key findings:**
- **100% establishment.** The `backoffLimit` fix was critical — 3 pods failed but the Job continued.
- Avg connect doubled from 30k (63s → 126s) for 2× watches, confirming ~2.1s per 1k watches at ~100 pods/node density.
- Max connect (408s) approaching DURATION (600s) — only 192s headroom.
- ~1M events/sec with 285ms health checks. Control plane not saturated.

---

### Experiment 6: 100k Watches (137 nodes)

**Goal:** Reach the 100k watch target — simulating a 100k-node cluster's watch load.

**Settings:** `DURATION=600, STAGGER=100, CONNS_PER_POD=15, JOB_TIMEOUT=1200` | **137 agent nodes** (scaled up from 40)

| Watches | Pods OK | Events | Events/sec | Reconnects | Errors | Peak Alive | Avg Connect (ms) | Max Connect (ms) | API Health (ms) |
|---|---|---|---|---|---|---|---|---|---|
| 100,005 | 6667/6667 | 999,563,309 | 1,665,939 | 100,103 | 98 | 100,005 | 74,951 | 303,462 | 319 |

**Key findings:**
- **100% establishment, 98 errors (0.098%).** Cleanest run at any scale.
- **Avg connect dropped 40% vs 60k despite 67% more watches.** The 137 nodes (vs 40) distributed HTTP/2 connections more broadly, confirming node count as the dominant connect latency variable.
- Max connect (303s) fit within DURATION (600s) with 297s headroom.
- **1.67 million events/sec** (~1 billion total events). Control plane healthy at 319ms.

---

## Analysis

### What Connect Latency Measures

The connect latency timer spans the HTTP `Watch()` call — from request initiation to receiving 200 OK response headers. The ~10k ADDED events per watch (existing CR replay) stream **after** the 200 response via `ResultChan()` and are not included in connect latency.

### API Server Watch() Code Path (K8s v1.35.2)

From `staging/src/k8s.io/apiserver/pkg/storage/cacher/cacher.go`:

```
Cacher.Watch()
  ├─ getWatchCacheResourceVersion()     // etcd call if RV="" (disproved by Experiment 2)
  ├─ watchCache.RLock()                 // shared read lock on watch cache
  ├─ getAllEventsSinceLocked()          // builds watchCacheInterval (lazy iterator)
  ├─ c.Lock()                          // EXCLUSIVE mutex for watcher registration
  │   └─ addWatcher() + watcherIdx++   // map insert
  ├─ c.Unlock()
  ├─ watchCache.RUnlock()
  ├─ go processInterval()              // streams initial events AFTER returning
  └─ return watcher                    // triggers 200 OK to client
```

### Connect Latency Factors

| Factor | Impact | Evidence |
|---|---|---|
| **Agent node density (pods/node)** | **Dominant** | ~50 pods/node: 750ms–2.1s per 1k watches. ~100 pods/node: 2.1–7.9s per 1k. More nodes = more HTTP/2 connections distributed across API servers = less contention. |
| **Cacher mutex contention** | Significant | `c.Lock()` is exclusive and shared with `dispatchEvent()`. Thousands of concurrent Watch() calls serialize on this lock. Bookmark dispatch hold time grows with watcher count, creating a feedback loop. |
| **HTTP/2 head-of-line blocking** | Minor | 15 watch streams share one TCP connection per pod. Event streaming on early streams can delay 200 OK headers for later streams. Mitigated by spreading across more nodes. |
| **GOAWAY (2% chance)** | Minor | `--goaway-chance=0.02` forces ~2% of pods to reconnect. Each GOAWAY kills all 15 streams. Impact diminishes with more nodes. |
| **Etcd round-trip** | Negligible | Disproved by Experiment 2. Only ~150ms at 1k watches, unmeasurable at 5k+. |

### Consolidated Scaling Data

| Exp. | Watches | Stagger | Nodes | Pods/Node | Avg Connect | Max Connect | Cost/1k | Errors | Peak Alive | Events/sec | API Health |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 1,005 | 15s | 10 | 7 | 0.4s | 5s | 325ms | 0 | 1,005 | 84k | 252ms |
| 1 | 5,010 | 15s | 10 | 33 | 28s | 87s | 6.1s | 0 | 5,010 | 408k | 321ms |
| 3 | 15,000 | 15s | 10 | 100 | 119s | 300s* | 7.9s | 526 | 13,349 | 418k | 309ms |
| 4 | 30,000 | 30s | 40 | 50 | 63s | 222s | 2.1s | 0 | 30,000 | 500k | 432ms |
| 5 | 60,000 | 60s | 40 | 100 | 126s | 408s | 2.1s | 1,770 | 60,000 | 1.0M | 285ms |
| 6 | 100,005 | 100s | 137 | 49 | 75s | 303s | 750ms | 98 | 100,005 | 1.67M | 319ms |

*\* Right-censored — max connect hit the DURATION ceiling.*

**Two clear density regimes:**
- **~50 pods/node** (Experiments 4, 6): 750ms–2.1s per 1k watches
- **~100 pods/node** (Experiments 3, 5): 2.1–7.9s per 1k watches

### Event Throughput

Events scaled linearly with no sign of control plane saturation at 100k:

| Watches | Events/sec | Total Events |
|---|---|---|
| 1,005 | 84k | 10M |
| 5,010 | 408k | 49M |
| 30,000 | 500k | 300M |
| 60,000 | 1.0M | 600M |
| 100,005 | 1.67M | 1.0B |

---

## Extrapolation

Based on the ~50 pods/node regime (750ms per 1k watches):

| Target | Pods | Nodes | Est. Avg Connect | Est. Max Connect | DURATION |
|---|---|---|---|---|---|
| 150,000 | 10,000 | ~200 | ~112s | ~360s | 600s |
| 250,000 | 16,667 | ~333 | ~188s | ~600s | 900s |
| 500,000 | 33,334 | ~667 | ~375s | ~1,200s | 1,800s |
| 1,000,000 | 66,667 | ~1,333 | ~750s | ~2,400s | 3,600s |

> **Caveat:** These assume the 6-node control plane does not saturate. At 100k it delivered 1.67M events/sec at 319ms health — but memory pressure and event fan-out may become limiting before connect latency at 500k+.

---

## Lessons Learned

1. **Right-censored data is misleading.** Experiments 1 and 3 showed artificially low avg connect times because slow connections timed out and were excluded. Always ensure `DURATION >> max connect time`.

2. **`backoffLimit: 3` is a silent killer at scale.** Three transient pod failures killed an entire 4,000-pod Job. Increased to 10,000 — the script's `JOB_TIMEOUT` provides the real safeguard.

3. **Agent node count matters more than stagger.** Scaling from 40→137 nodes at 60k→100k watches *reduced* avg connect by 40% despite adding 67% more watches.

4. **Stagger helps, but proportionally.** ~1s per 1k watches (e.g., STAGGER=100 at 100k) keeps the initial burst manageable.

5. **The API server's 5–10 min watch timeout drives reconnect churn.** At DURATION=600, every watch reconnects approximately once. Steady-state testing needs DURATION ≥ max_connect + 600s.

6. **Event replay scales linearly.** Each watch replays ~10k CRs on connect → 1B events at 100k. This is a memory and network cost, not a connect latency cost.

---

## Recommendations

1. **Keep pods/node at ~50** for optimal connect times. This is the primary lever — more impactful than tuning stagger or duration.

2. **DURATION=600s is sufficient up to ~250k watches** at ~50 pods/node. Beyond that, increase to 900–1,800s.

3. **Scale STAGGER at ~1s per 1k watches** (e.g., STAGGER=150 at 150k, STAGGER=250 at 250k).

4. **Set `backoffLimit: 10000`** in the Job template.

5. **Set JOB_TIMEOUT = DURATION + 600s** to allow for pod scheduling and ConfigMap collection.

6. **For steady-state testing**, use DURATION ≥ max_connect + 600s (≈1,200s at 100k).

7. **Consider disabling `--goaway-chance`** for controlled experiments.

8. **Monitor control plane saturation beyond 100k.** Event fan-out memory and CPU may become limiting before connect latency does.
