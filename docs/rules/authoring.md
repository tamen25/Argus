# Rule authoring

Rules are data: YAML + [CEL](https://github.com/google/cel-spec). Adding a
common-case rule requires zero Go changes. The loader is strict — unknown
fields, bad enums, invalid CEL, or duplicate IDs fail at load time.

## Schema (`argus.rules/v1`)

```yaml
schema: argus.rules/v1        # required, exactly this version
id: RES-005                   # unique, stable
source: spec                  # spec | argus (extensions never affect the spec score)
name: service.name is present
description: >-               # shown verbatim in findings
  Resource attributes MUST contain a non-empty service.name.
target: resource              # resource | span | metric | log
impact: critical              # critical(40) | important(30) | normal(20) | low(10)
evaluation:
  mode: item                  # item | aggregate
  # aggregate: metric_attribute_cardinality   # required when mode: aggregate
  criteria: "'service.name' in resource && string(resource['service.name']) != ''"
service_violation:            # optional; item mode only
  threshold_ratio: 0          # service fails when violations/observed > ratio (default 0)
params: {}                    # values exposed to CEL as params.<key>
confidence:                   # optional backend verification
  poller: mimir_service_presence   # or mimir_label_cardinality
remediation:
  template: missing-service-name   # patch template name (informative in Phase 1)
```

`criteria` evaluates to **true on success** (mirroring the spec's Criteria
attribute); false is a violation. It must type-check as `bool`.

## CEL variables

| Variable | Type | Available |
|---|---|---|
| `kind` | string (`span`/`metric`/`log`) | item mode |
| `service` | string | both |
| `resource` | map | item mode — resource attributes as observed |
| `scope` | map (`name`, `version`) | item mode |
| `span` | map (`name`, `kind`, `has_parent`, `status`, `attrs`) | item mode, span items |
| `metric` | map (`name`, `type`, `unit`, `has_exemplars`, `exemplar_count`, `bucket_bounds`, `bucket_counts`, `attrs`) | item mode, metric items |
| `log` | map (`severity_text`, `severity_number`, `has_trace_id`, `body_len`, `attrs`) | item mode, log items |
| `agg` | map — fields of the aggregate row | aggregate mode |
| `params` | map — the rule's `params` block | both |

Attribute values are truncated to 256 bytes before evaluation (bounded
memory); write criteria accordingly.

## Aggregates

Aggregate-mode rules evaluate named rows computed by the ingest layer:

| Aggregate | Fields |
|---|---|
| `metric_attribute_cardinality` | `metric`, `attribute`, `cardinality` (HLL estimate) |

New aggregate *types* require Go; new thresholds/criteria over existing
aggregates do not.

### Window semantics (all aggregates inherit this)

Sketches cannot subtract, so aggregate windows are **two-generation
tumbling**, not sliding: observations land in the *current* generation; every
window length W (engine flag `--cardinality-window`, default 1h) the current
generation becomes *previous* and a fresh current starts. Reported estimates
are **max(current, previous)**.

Consequences: a finding never vanishes at a window boundary; a value set
stops influencing estimates only after being silent for between W and 2W;
short bursts can therefore be visible for up to 2W. A rule's `params.window`
documents the intended window and must match the engine setting — the engine
does not derive per-rule windows.

### Bounds (all aggregates inherit this)

The sketch store is hard-capped at `--max-tracked-pairs` (default 4096) per
generation with **LRU admission**: a new pair evicts the least recently
observed one. Pressure is observable, never silent:
`argus_aggregate_pairs_tracked` (gauge), `argus_aggregate_pair_evictions_total`
(counter), plus a report note when evictions occurred. Memory envelope:
worst case ≈ cap × 16KiB (dense HLL-14) × 2 generations; in practice far less
(low-cardinality pairs stay sparse).

## Rollup, scoring, confidence

Per service: an item rule fails when `violations/observed > threshold_ratio`.
Scores follow the Instrumentation Score spec formula exactly
(`Σ(P×W)/Σ(T×W)×100`, weights 40/30/20/10) over `source: spec` rules only;
`source: argus` rules produce findings and a separate extension score.
Findings carry `confidence: sampled` (OTLP mirror) or `verified` (poller saw
unsampled backend data). Every rule needs a golden-file test under
`engine/internal/rules/testdata/golden/` — input OTLP JSON in, expected
snapshot out (`go test ./internal/rules -run TestGolden -update` after review).

## Calibration (optional)

A rule may declare which observed distribution can propose a better value
for **one** params key. `argus rules calibrate` uses it; the evaluation
path ignores it entirely.

```yaml
calibration:
  param: max_span_names        # params key — or the dotted literal
                               # service_violation.threshold_ratio
  source: aggregate            # aggregate | finding_ratio
  aggregate: span_name_cardinality   # required when source=aggregate
  field: cardinality                 # numeric field in the row
  kind: count                  # count | small_count | ratio (formula)
```

Criteria are never calibrated. For spec rules, only params the spec leaves
open may carry a calibration block. Formulas and caveats:
[Making the thresholds yours](../making-thresholds-yours.md).
