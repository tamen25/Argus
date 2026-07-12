# `argus score`

Evaluates instrumentation quality and prints an Instrumentation Score report.
One-shot: collect → evaluate → report → exit. Exit code is non-zero when
`--fail-below-score` trips — the CI gate use case.

```bash
# stream + poller, markdown to stdout, gate at 85
argus score \
  --listen-otlp :4317 --window 60s \
  --mimir-url http://mimir-gateway.lgtm.svc \
  --fail-below-score 85

# JSON for machines, persisted history
argus score --listen-otlp :4317 --output json --store-dsn postgres://argus:...@db/argus
```

| Flag | Default | Meaning |
|---|---|---|
| `--rules` | `rules` | rule directory (expects `spec/` and `argus/` subtrees) |
| `--listen-otlp` | *(off)* | OTLP gRPC address receiving the **sampled mirror** (point a second Alloy exporter here; Argus is never in the critical path) |
| `--window` | `60s` | collection window while listening |
| `--mimir-url` | *(off)* | enables poller verification (`confidence: verified`) |
| `--mimir-tenant` | *(none)* | `X-Scope-OrgID` header |
| `--output` | `markdown` | `markdown` or `json` |
| `--out` | stdout | write report to a file |
| `--fail-below-score` | `0` (off) | non-zero exit when fleet score is below this |
| `--store-dsn` | *(off)* | Postgres DSN; persists snapshot + findings (schema migrates automatically) |
| `--spec-version-file` | `.instrumentation-score-version` | pinned spec version echoed in reports |

**Report honesty guarantees:** every report carries the spec version, the
incomplete-rule-set disclosure (until all official rules are implemented),
per-finding confidence (`sampled`/`verified`), and notes for anything degraded
(poller failures, cardinality-tracker overflow). Verification failures never
hide sampled findings.

For continuous evaluation with Prometheus export
(`argus_instrumentation_score{service=...}`), see `argus serve --otlp-grpc :4317`.
