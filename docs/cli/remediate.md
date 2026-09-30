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
| `--explain` | off | also write an LLM prose explanation per finding (needs `--llm-endpoint`) |
| `--llm-endpoint` | | OpenAI-compatible chat completions URL |
| `--llm-model` | | model name |
| `--llm-api-key-env` | `OPENAI_API_KEY` | env var holding the API key |
| `--llm-no-redact` | off | send attribute values to the LLM (redaction is **on** by default) |

Rendering is deterministic and template substitution uses only the
finding's own evidence (metric/attribute names, observed cardinality,
violation ratio). Where evidence lacks a value the patch says
`REPLACE_WITH_…` rather than guessing.

## Templates

Every rule names a template, and a test fails if one does not exist.

| Template | Rules | What the patch does |
|---|---|---|
| `missing-service-name` | RES-005, ARG-RES-001 | tags telemetry missing `service.name` (stopgap; the real fix is `OTEL_SERVICE_NAME`) |
| `missing-resource-attributes` | ARG-RES-002, ARG-RES-003, ARG-RES-004 | adds Kubernetes identity (`k8sattributes`), `service.version` from the pod's `app.kubernetes.io/version` label, and a default `deployment.environment.name` |
| `high-cardinality-attribute` | MET-001 | drops the offending attribute on the offending metric |
| `missing-metric-unit` | MET-002 | sets the unit on one metric (you fill in the UCUM code) |
| `unit-in-metric-name` | MET-005 | renames the metric and records its unit, with a warning that renaming breaks existing queries |
| `histogram-bucket-mismatch` | MET-004, ARG-MET-002 | guidance only: buckets are chosen by the SDK, so the fix is an SDK View |
| `missing-exemplars` | ARG-MET-001 | makes sure the remote write keeps exemplars; the SDK has to record them |
| `broken-context-propagation` | SPA-002, SPA-004, ARG-SPA-002 | guidance only: a collector cannot repair trace context, so it lists the fixes in the workload |
| `unbounded-span-name` | SPA-003 | normalizes IDs/UUIDs/hex in span names |
| `logs-without-trace-context` | ARG-LOG-001 | recovers `trace_id` printed in log bodies; states plainly that only the app can fix the rest |
| `log-level-abuse` | LOG-001 | drops DEBUG-and-below in prod environments |
| `log-severity-unset` | LOG-002 | infers a severity from level words in the body (a heuristic stopgap) |

Each template's header states the *preferred* fix (usually SDK-side) and what
the collector-side patch costs you. Where a collector cannot fix the problem at
all, the template says so and generates no configuration rather than a patch
that only looks like a fix. Every Collector form is checked with
`otelcol-contrib validate`, and every Alloy form with `alloy fmt`.

## LLM explanations (`--explain`)

With `--explain` and an OpenAI-compatible endpoint, Argus writes a
`<service>-<template>.explanation.md` next to each patch — plain-prose context
for *why* the finding matters and *how* the patch fixes it.

```bash
export OPENAI_API_KEY=sk-…
argus remediate --report report.json --finding MET-001 --explain \
  --llm-endpoint https://api.example.com/v1/chat/completions --llm-model gpt-x
```

The LLM sits strictly at the **edge** (architecture rule 2): it explains the
already-generated deterministic patch and **never changes the patch or the
score**. Any OpenAI-compatible endpoint works — a remote API or a self-hosted
compatible server.

**Redaction is on by default** (rule 8): attribute *values* are stripped
before anything reaches the endpoint (keys are kept so the model sees the
shape), and free-text evidence summaries are dropped. `--llm-no-redact` opts
out explicitly and is reported in the run summary. Prompt *templates* are
versioned with golden tests; model *output* is never tested — it's prose, not a
control signal.
