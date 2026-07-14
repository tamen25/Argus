# `argus remediate`

Renders a finding's remediation template into patch files — **Alloy
(River)** and **OTel Collector YAML** — that a human reviews and applies.
Argus is a read-only product: it generates files, it never touches your
systems, and every rendered patch carries a review notice.

```bash
argus score --listen-otlp :4317 --output json --out report.json
argus remediate --report report.json --finding MET-001
argus remediate --report report.json --finding ARG-LOG-001 --service checkout
```

| Flag | Default | Meaning |
|---|---|---|
| `--report` | *(required)* | JSON report from `argus score --output json` |
| `--finding` | *(required)* | rule ID to remediate (all failing services unless `--service`) |
| `--service` | *(all)* | scope to one service |
| `--rules` | *(built-ins)* | extra rule dir (same override semantics as `score`) |
| `--out` | `remediations` | output directory: `<service>-<template>.alloy.river` / `.collector.yaml` |

Rendering is deterministic and template substitution uses only the
finding's own evidence (metric/attribute names, observed cardinality,
violation ratio). Where evidence lacks a value the patch says
`REPLACE_WITH_…` rather than guessing.

## Shipped templates (Phase 1)

The five committed in the master plan (§6.3 — rules 1, 2, 4, 5, 7 by plan
numbering):

| Template | Rules | What the patch does |
|---|---|---|
| `missing-service-name` | RES-005, ARG-RES-001 | tags telemetry missing `service.name` (stopgap; the real fix is `OTEL_SERVICE_NAME`) |
| `high-cardinality-attribute` | MET-001 | drops the offending attribute on the offending metric |
| `logs-without-trace-context` | ARG-LOG-001 | recovers `trace_id` printed in log bodies; states plainly that only the app can fix the rest |
| `unbounded-span-name` | SPA-003 | normalizes IDs/UUIDs/hex in span names |
| `log-level-abuse` | LOG-001 | drops DEBUG-and-below in prod environments |

Rules whose templates are not yet shipped fail with an explicit
unknown-template error listing what exists. Each template's header states
the *preferred* fix (usually SDK-side) and what the collector-side patch
costs you — honesty over convenience, always.

LLM-drafted explanation text arrives in Phase 2 and will never modify these
deterministic patches.
