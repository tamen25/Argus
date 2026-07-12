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
| `metric` | map (`name`, `type`, `unit`, `has_exemplars`, `attrs`) | item mode, metric items |
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

## Rollup, scoring, confidence

Per service: an item rule fails when `violations/observed > threshold_ratio`.
Scores follow the Instrumentation Score spec formula exactly
(`Σ(P×W)/Σ(T×W)×100`, weights 40/30/20/10) over `source: spec` rules only;
`source: argus` rules produce findings and a separate extension score.
Findings carry `confidence: sampled` (OTLP mirror) or `verified` (poller saw
unsampled backend data). Every rule needs a golden-file test under
`engine/internal/rules/testdata/golden/` — input OTLP JSON in, expected
snapshot out (`go test ./internal/rules -run TestGolden -update` after review).
