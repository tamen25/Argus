# Validating scenario 1 (cardinality-explosion-checkout)

What has to be true on a live cluster before this scenario's numbers mean
anything, and how to check each one. Written because the fault manifest was
authored against otel-demo chart 0.40.9 **without** a cluster to test on: the
mechanism is reasoned from the chart, not observed, and reasoning is not
evidence.

## Prerequisites

```bash
make dev-up                       # kind: LGTM + otel-demo + chaos-mesh
ollama create qwen3.6-bench -f deploy/ollama/Modelfile.qwen3.6-bench
```

## 1. The fault applies and removes cleanly

```bash
kubectl apply -f scenarios/faults/cardinality-explosion.yaml
kubectl -n otel-demo get pods -l argus.dev/fault=cardinality-explosion
kubectl delete -f scenarios/faults/cardinality-explosion.yaml --ignore-not-found
```

Both must succeed. The manifest is **additive only** — a ConfigMap and a
Deployment it owns. That is not a stylistic choice: the kubectl injector runs
`kubectl delete -f <manifest>` on reset and cleanup, so a manifest that patched
the checkout Deployment would inject correctly and then be "cleaned up" by
deleting the real workload. Any fault needing mutation of a pre-existing
resource must be a `script` step instead (see `scenarios/faults/toggle-flag.sh`,
which patches a ConfigMap and does its own rollout restart).

## 2. The driver actually reaches checkout

The single biggest unvalidated assumption. The driver POSTs to
`frontend-proxy:8080/api/cart` then `/api/checkout` with a unique `userId` per
request.

```bash
kubectl -n otel-demo logs -l argus.dev/fault=cardinality-explosion --tail=20
kubectl -n otel-demo exec deploy/argus-fault-cardinality -- \
  curl -s -o /dev/null -w '%{http_code}\n' http://frontend-proxy:8080/api/cart
```

If the API shape has moved between chart versions, fix the request bodies in the
ConfigMap rather than pointing the scenario at a different service — the ground
truth names checkout, so checkout is what must be driven.

## 3. Cardinality actually grows, and on checkout

The assumption is that checkout records the caller's user id as a metric
attribute, so a unique id per request becomes one series per user. **Verify it
rather than believing it.** Against Mimir:

```promql
count({__name__=~".+", service_name="checkout"})
```

Sample before injecting and again after ~5 minutes. If the count is flat, the
attribute is not on the metric path and the mechanism needs rework: the next
thing to try is an unbounded dimension checkout definitely records (inspect with
`count by (__name__) ({service_name="checkout"})` and look for a label whose
cardinality tracks request count).

Also confirm the growth is attributed to **checkout** and not to frontend-proxy
alone — if only the edge service explodes, the scenario's ground truth is wrong
and should either move or the driver should target checkout directly.

## 4. Steady state before the agent is asked

Currently **not enforced**. The orchestrator uses `AlwaysReadyProbe`, so the
agent is handed the incident the instant `kubectl apply` returns — before a
single scrape interval has passed, and therefore before the telemetry shows
anything. On a live run every agent would score near zero for reasons that have
nothing to do with its ability.

Until a telemetry-backed steady-state probe exists, inject and wait manually,
then score with `--inject=none`:

```bash
kubectl apply -f scenarios/faults/cardinality-explosion.yaml
sleep 300                                     # let series accumulate and scrape
argus bench run --scenario scenarios/cardinality-explosion-checkout.yaml \
  --agent openai --endpoint http://127.0.0.1:11434/v1/chat/completions \
  --model qwen3.6-bench \
  --mimir-url http://<mimir-gateway>:80 \
  --inject none --repeats 3 --env-digest kind-$(date +%F)
kubectl delete -f scenarios/faults/cardinality-explosion.yaml
```

A report produced this way must not claim Argus injected the fault — it did not,
and `--inject=none` is recorded precisely so the report cannot imply otherwise.

## 5. Calibrate before believing any agent number

```bash
argus bench run --scenario scenarios/cardinality-explosion-checkout.yaml \
  --agent stub --stub-profile obvious --inject none --repeats 3
```

Expected: **0.00**. If the obvious-guess stub scores respectably against a live
environment, the rubric drifted and the scenario needs tightening before its
results are published. See [the CLI guide](../cli/bench.md) for the full profile
table.

## Known gaps

| Gap | Effect | Status |
|---|---|---|
| Driver request shape unverified against a live cluster | Fault may not fire at all | Blocks step 2 |
| Cardinality attribution to checkout unverified | Ground truth may name the wrong entity | Blocks step 3 |
| No steady-state probe | Agent asked before telemetry exists | Work around with `--inject=none` + manual wait |
| Evidence checked for form, not truth | A fabricated citation scores full marks | By design; documented in every report |
