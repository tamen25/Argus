# Authoring bench scenarios

The rules here are not style preferences. Each one is a failure that already
happened, or one the harness cannot protect you from.

## 1. Manifest faults must be additive

The kubectl injector runs `kubectl apply -f` on inject and `kubectl delete -f` on
reset and cleanup. It deletes whatever the manifest declares.

So a manifest may only **create disposable objects** — a Chaos Mesh CR, a Job, a
driver Deployment the fault owns. The natural way to fault a running workload is
to apply a patched copy of it, and that would inject correctly and then be
"cleaned up" by deleting the real Deployment.

A fault that must mutate a pre-existing resource is a `script` step, paired with
a restore hook (rule 6).

## 2. Verify the telemetry on a warm cluster before writing the ground truth

Scenario 1 originally named checkout as the root cause of a metric cardinality
explosion. On a cluster about ten minutes old, checkout showed 10 series and no
request metrics, and that was written up as "checkout emits no request metrics".
It was a cold-cluster artifact: nine hours later checkout emitted 226 series
including `rpc_server_*`. Request metrics appear once a service has served
traffic.

The scenario was still unachievable, for a different reason: checkout's request
metrics are **bounded** (its one per-request dimension, `rpc_method`, has a single
value), so there is nothing to explode. frontend's `target` label carries the raw
URL path, which is.

Before committing to a ground truth, ask a cluster that has been up for a while
what the workload emits, and which of its labels are unbounded:

```promql
count by (__name__) ({job="<service>"})
```

The service label on this stack is **`job`**, not `service_name` — only flagd
reports under `service_name`.

## 3. Gate on the signal the fault actually produces — measured, not assumed

Every scenario needs a `steadyState` gate. It is the harness's main protection
against a fault that does not bite and a selector that quietly matches nothing: a
fault that never reaches steady state fails the run instead of scoring an agent
against a healthy cluster. It also keeps the agent from being asked before a
scrape interval has passed.

The gate only works if it watches what the fault really does. Two scenarios were
first written with a 5xx-rate gate because "the storefront will throw errors".
Measured live, a 4s Redis delay drove frontend p95 from 21 ms to 10 s and
produced **zero** 5xx — requests succeed slowly — so the gate could never fire
and the scenario could never run. The same was true of a network partition. Only
the egress blackhole actually errors.

| Fault shape | Signal | Example |
|---|---|---|
| latency, partition | p95 latency | `histogram_quantile(0.95, sum by (le) (rate(http_server_duration_milliseconds_bucket{job="frontend"}[2m])))` `min: 500` |
| hard errors | 5xx rate | `sum(rate(app_frontend_requests_total{job="frontend",status=~"5.."}[2m]))` `min: 0.01` |
| outage | absence | `absent(count_over_time(go_goroutine_count{job="checkout"}[2m]))` `min: 1` |
| cardinality | live series | `count(count_over_time(app_frontend_requests_total{job="frontend"}[2m]))` `min: 300` |

Inject the fault by hand once and watch the candidate query before writing it
down.

**Use windows that forget.** Before each repeat the orchestrator waits until the
signature is observably *absent*. A plain instant query keeps counting a series
for Prometheus's 5-minute lookback after it stops being written; wrapping it in
`count_over_time(...[2m])` or using a `[2m]` rate window lets the baseline clear
in about two minutes instead of five (measured on scenario 1: 121 s vs 302 s).
Keep the window at least twice the export interval (the otel-demo SDKs export
every 60 s) or a live series will flicker in and out.

**NaN is not a reading.** `histogram_quantile` returns NaN when no requests fell
in the window. The probe treats NaN as "unknown" — it neither passes the gate nor
proves a clean baseline — but a query that is NaN most of the time will simply
never fire.

## 4. Decoys are the scenario's actual difficulty

Ground truth with a single entity is one guess against a service list the agent
can see without any telemetry. Decoys are what make it a diagnosis.

Pick them from what a lazy reading actually produces:

| Scenario | Decoy | Why it is tempting |
|---|---|---|
| cardinality explosion | `product-reviews` | its name is inside the exploding label value |
| egress blackhole in frontend | `product-catalog` | frontend's own errors name it as unreachable |
| network partition | `recommendation` | loudest casualty, calls the partitioned service constantly |
| redis latency | `cart` | where the symptom is loudest, one hop from the cause |
| OOMKill | `kafka` | goes quiet downstream of checkout, reads like a broker problem |

A decoy that is also ground truth is rejected by the loader — it would penalize
the correct answer.

## 5. Calibrate, then validate the lifecycle

Rubric first, with no cluster (`--inject none`):

```bash
argus bench run --scenario scenarios/<name>.yaml --agent stub \
  --stub-profile obvious --stub-obvious <ground-truth-name> \
  --stub-category <ground-truth-category> --inject none
```

| Profile | Expected | Meaning |
|---|---|---|
| `vague` | no diagnosis | names no entity; fails validation upstream |
| `obvious` | **0.00** | right entity, wrong category, no evidence |
| `shotgun` | **0.00** | dilution plus decoy penalties — pass each decoy with `--stub-shotgun` |
| `cited` | **1.00** | right entity + category + fabricated evidence: the honest ceiling |

`obvious` above zero means the rubric is too loose; `cited` below 1.00 means it is
too tight. Both are bugs in the scenario, not the agent.

Then the mechanics, against the live cluster and still at no model cost: give the
stub `--mimir-url` and it takes the full lifecycle a real agent does — reset,
baseline, inject, steady state, answer, cleanup.

```bash
argus bench run --scenario scenarios/<name>.yaml --agent stub --stub-profile cited \
  --stub-obvious <gt> --stub-category <cat> --mimir-url http://127.0.0.1:18080 \
  --inject-namespace otel-demo --kube-context kind-argus --repeats 2
```

Both repeats should score 1.00 with no error, and the second should take about as
long as the first — a second repeat that finishes in seconds passed its gate on
the first one's residue.

## 6. A fault with lasting effects needs a restore hook

Deleting a fault's objects is not always enough. Deleting scenario 1's load
driver stops new series, but frontend's OTel SDK keeps exporting every series the
driver created until frontend restarts. Declare the restore in the scenario:

```yaml
spec:
  reset:                                   # before every repeat
    - script: faults/restart-frontend.sh
  cleanup:                                 # after every repeat, even a failed one
    - script: faults/restart-frontend.sh
```

For a script that mutates a workload, **record the originals on the object
itself** before changing anything, and have the restore put back exactly those —
and remove the record only after the rollout succeeds, so a failed restore can
simply run again. See `faults/oomkill-checkout.sh` and
`faults/restore-checkout-memory.sh`.

## 7. Every fault object carries the sweep label

```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: argus-bench
```

Reset sweeps every object with this label, cluster-wide, not only the ones the
current scenario declares. That is what catches a fault leaked by a crashed run
or a manual test: a StressChaos applied by hand on 2026-07-25 sat on checkout for
two months, restarting it repeatedly, and no scenario's reset or baseline check
could see it. A test fails if any fault manifest object lacks the label.

## 8. Do not break the telemetry the agent needs

A fault that cuts the OTel exporter's path to the collector destroys its own
evidence trail and scores every agent zero. Scope faults so the telemetry
pipeline survives — the egress blackhole targets three named backends rather than
everything.

## Environment gotchas

- **kind's default CNI (kindnet) does not enforce NetworkPolicy.** The object is
  created, reports healthy, and blocks nothing. Use a Chaos Mesh `partition`.
- **DNSChaos does not inject** (chaos-mesh 2.8.3): chaos-daemon's helper panics
  rewriting the target's resolv.conf and the CR reports `AllInjected=False`.
- **StressChaos cannot OOM-kill the workload.** The OOM killer picks the largest
  process in the cgroup, which is the stressor. Lower the workload's own memory
  limit instead.
- **A single-replica RollingUpdate protects the fault from biting.** The old
  healthy pod stays until the new one is Ready, and a crashlooping pod never is.
  Switch the strategy to `Recreate` for the fault and restore it afterwards.
- **Chaos Mesh CRs should not set `spec.duration`.** The orchestrator owns the
  fault's lifetime; a self-expiring CR lets the environment heal underneath a
  still-running agent.
- **Chaos Mesh reports success when a selector matches nothing.** Rule 3's gate
  is what catches it.
