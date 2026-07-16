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
