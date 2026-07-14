# Soak testing

A soak run answers the questions a short window can't: does memory stay
flat for hours (architecture rule 3), do the two-generation windows rotate
cleanly across many boundaries, does the aggregate store hold under
sustained LRU pressure — and what do the *real* distributions behind every
numeric rule threshold look like, so `argus rules calibrate` proposes
evidence-based overrides instead of guesses.

## Running

```bash
make soak                 # 24 hours (the real thing)
SOAK_HOURS=2 make soak    # shorter smoke of the harness itself
```

The harness (`scripts/soak.sh`):

1. builds the engine image (spec pin baked in via `SPEC_VERSION` build arg),
   loads it into the kind cluster, applies `deploy/kind/argus-engine.yaml`;
2. wires the Alloy mirror by `helm upgrade`-ing with the repo values
   (`deploy/kind/values/alloy.yaml` now ships the `otelcol.exporter.otlp
   "argus"` fan-out — Argus stays out of the critical path: a dead engine
   only drops mirror data);
3. samples engine self-metrics **every minute** via the apiserver service
   proxy (no port-forward to babysit for 24h) into `metrics.csv`:
   RSS, Go heap, `argus_aggregate_pairs_tracked`,
   `argus_aggregate_pair_evictions_total`, `argus_items_consumed_total`
   per signal, goroutines;
4. snapshots `/api/report` and `/api/aggregates` **every hour**, and greps
   engine logs for receiver errors.

Output lands in `soak-output/<UTC timestamp>/` (gitignored).

## Success criteria

- **Flat memory after warmup** — first-quarter vs last-quarter RSS median
  within 15%. The Deployment's 256Mi limit is part of the test: an OOMKill
  is a failed soak.
- **Window rotation observable** — `pairs_tracked` must sawtooth at
  boundaries, never grow monotonically.
- **No receiver errors** — `engine-errors.log` stays empty; engine pod has
  zero restarts (`final-pods.txt`).

## Analyzing

```bash
make soak-analyze SOAK_DIR=soak-output/<ts>
```

Prints the verdicts above plus threshold-relevant distributions
(median/MAD-free robust stats: median, P90, P99, max):

| Distribution | Feeds rule |
|---|---|
| span_name_cardinality per service | SPA-003 (`max_span_names`) |
| metric_attribute_cardinality per (service, metric, attribute) | MET-001 (`max_cardinality`) |
| resource attr value counts per key | ARG-RES-004 (`max_values`) |
| exemplar coverage per service | ARG-MET-001 |
| orphan / missing-root ratios | SPA-002 / ARG-SPA-002 |
| per-rule violation ratios from hourly reports | ARG-LOG-001 and friends |

Caveat the analyzer states itself: report-derived ratios cover **failing
services only** — passing services carry no stats in reports. The
aggregate-derived distributions cover every tracked service.

Feed the output dir to calibration once it exists (Phase 1):
`argus rules calibrate --soak-dir soak-output/<ts>`.
