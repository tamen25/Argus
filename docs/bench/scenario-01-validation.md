# Validating scenario 1 (cardinality-explosion-frontend)

A record of validating scenario 1 against a live kind cluster on 2026-07-25
(otel-demo chart 0.40.9, LGTM via `make dev-up`), including the assumption that
turned out to be wrong and how it was found.

## What the validation changed

The scenario originally named **checkout** as ground truth. It could not work,
but the reason first recorded here was wrong, and the way it was wrong is the
more useful lesson.

**First reading (wrong).** On a cluster about ten minutes old, checkout showed
only Go runtime metrics plus `target_info` — 10 series, no request metrics at
all — and that was written up as "checkout emits no request metrics".

**What is actually true.** Nine hours later the same query returned 226 series,
including `rpc_server_*` and `rpc_client_*` histograms. Request metrics appear
once a service has *served traffic*; ten minutes in, checkout had not yet. The
reading was a cold-cluster artifact. Always check a ground truth against a warm
cluster.

**Why checkout still cannot be the target.** Its request metrics are bounded.
The only per-request dimension is `rpc_method`, and it has one value
(`PlaceOrder`), alongside a service name and a gRPC status code. There is nothing
unbounded to explode, so no volume of traffic produces a cardinality incident
there. The retarget to frontend stands; only its stated reason changed.

frontend (Node.js) emits request metrics that carry an unbounded dimension:

```
app_frontend_requests_total{job="frontend", method="GET", status="200",
                            target="/api/product-reviews/0PUK6V6EV0"}
```

`target` holds the **raw URL path**. The product-reviews route takes a product
id as a path segment, so every distinct id is its own series — the classic
instrumentation defect, on a legitimate route with a legitimate label and no
aggregation over the high-cardinality dimension.

**Measured:** 60 unique paths produced exactly 60 new series (38 → 101 total),
attributed to `job="frontend"`. Ground truth moved to frontend and the scenario
was renamed. See DECISIONS.md.

Note the service label is **`job`**, not `service_name` — only flagd reports
under `service_name` on this stack. Worth checking before writing any PromQL for
a new scenario.

## Re-running the validation

```bash
make dev-up
```

### 1. Fault applies and removes cleanly

```bash
kubectl apply --dry-run=server -f scenarios/faults/cardinality-explosion.yaml
kubectl apply -f scenarios/faults/cardinality-explosion.yaml
kubectl -n otel-demo get pods -l argus.dev/fault=cardinality-explosion
kubectl delete -f scenarios/faults/cardinality-explosion.yaml --ignore-not-found
```

The manifest is **additive only** — a ConfigMap and a Deployment it owns. Not a
stylistic choice: the kubectl injector runs `kubectl delete -f <manifest>` on
reset and cleanup, so a manifest that patched the frontend Deployment would
inject correctly and then be "cleaned up" by deleting the real workload. Faults
needing mutation of a pre-existing resource must be `script` steps instead (see
`scenarios/faults/toggle-flag.sh`, which patches a ConfigMap and restarts flagd
itself).

### 2. Cardinality grows, on frontend

```promql
count(app_frontend_requests_total{job="frontend"})
```

Ambient is ~40. Three drivers at ~4 req/s add roughly 700 series/minute;
observed crossing 300 about 90s after injection, which includes Alloy batching
and Mimir ingestion delay.

### 3. Steady state is enforced, not assumed

The scenario declares:

```yaml
steadyState:
  query: count(app_frontend_requests_total{job="frontend"})
  min: 300
  settle: 30s
```

The agent is not asked anything until that holds for the settle window. Without
it the agent is handed the incident the instant `kubectl apply` returns — before
a scrape interval has passed, so before the fault is visible in any backend —
and scores near zero for reasons that have nothing to do with its ability. That
was a real gap in the orchestrator, not a hypothetical one; `AlwaysReadyProbe`
remains the behavior only for scenarios that declare no `steadyState`.

### 4. Calibrate before believing any agent number

```bash
argus bench run --scenario scenarios/cardinality-explosion-frontend.yaml \
  --agent stub --stub-profile obvious --stub-obvious frontend \
  --inject none --inject-namespace otel-demo --repeats 3
```

Expected **0.00**: the stub names the correct entity but cites nothing.

| Profile | Entity | Score | Why |
|---|---:|---:|---|
| `vague` | — | no diagnosis | names no entity; fails validation upstream |
| `obvious` | 1.00 | **0.00** | right workload, wrong category, no evidence |
| `shotgun` | 0.25 | **0.00** | dilution across five guesses |
| `cited` | 1.00 | **1.00** | right entity + category + *fabricated* evidence |

`cited` scoring 1.00 is expected and is the honest ceiling: the scorer checks
citations are present and well-formed, never that they are true.

### 5. Full run

```bash
argus bench run --scenario scenarios/cardinality-explosion-frontend.yaml \
  --agent openai --endpoint https://api.example/v1/chat/completions \
  --model my-model --api-key-env MY_API_KEY \
  --mimir-url http://127.0.0.1:18080 \
  --inject-namespace otel-demo --kube-context kind-argus \
  --repeats 3 --max-tool-calls 15 --env-digest kind-argus-$(date +%F)
```

## Running the tooling from Windows against a WSL cluster

`make dev-up` runs in WSL2 (the kind config mounts `/var/lib/argus/history` from
the WSL filesystem). Docker Desktop publishes the kind API port to Windows as
well, so `argus` can also run on Windows against the WSL cluster.

```bash
wsl -d Ubuntu-24.04 -e bash -c 'kind get kubeconfig --name argus' > argus.kubeconfig
kubectl --kubeconfig argus.kubeconfig port-forward -n lgtm svc/mimir-gateway 18080:80 &
export KUBECONFIG=$PWD/argus.kubeconfig
```

Mimir is then on Windows loopback at port 18080.

## Remaining gaps

| Gap | Effect | Status |
|---|---|---|
| Evidence checked for form, not truth | A fabricated citation scores full marks | By design; asserted by test, printed in every report |
| `shotgun` stub profile uses generic service names | It does not exercise this scenario's decoys via the CLI | Decoy penalty is covered by unit test instead |
| Injector cannot express restart-requiring faults | Such faults must be `script` steps | Documented; revisit if many scenarios need it |
