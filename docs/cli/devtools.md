# argus devtools

Development and demo utilities. Not product features: these write local
files for your own environments.

## `argus devtools synth-history`

Fabricates multi-week metric history with embedded incident signatures —
real TSDB blocks plus a matching `incidents.yaml`, so the ground truth ships
with the data. Exists because real dev history accumulates in short, gappy
sessions (measured: 36 hours across 6 calendar days — see
[Backtest fidelity](../backtest-fidelity.md)) while demos and CI need
continuous windows with known answers.

```bash
argus devtools synth-history --spec synth.yaml --out demo-history/
```

Spec (`argus.synth/v1`, strict loader — unknown keys are errors):

```yaml
schema: argus.synth/v1
seed: 42                      # same spec + seed → identical output
from: 2026-01-01T00:00:00Z
to:   2026-01-21T00:00:00Z    # three continuous weeks
step: 30s
services:
  - name: checkout
    rate_per_sec: 10
    error_ratio: 0.005        # steady state
    jitter: 0.1               # per-sample variation
incidents:
  - id: 2026-01-07-checkout-errors
    service: checkout
    start: 2026-01-07T14:00:00Z
    end:   2026-01-07T14:45:00Z
    error_ratio: 0.30         # the embedded signature
```

Output:

- `<out>/blocks/` — TSDB blocks emitting `synthetic_requests_total` and
  `synthetic_errors_total` (cumulative counters, `service` label), readable
  by anything that reads Prometheus blocks;
- `<out>/incidents.yaml` — schema-v1 registry matching the embedded
  signatures, ready for `argus backtest run --incidents`.

Load into a dev Mimir by copying blocks into the tenant's bucket (e.g.
`mc cp --recursive blocks/ h/mimir-tsdb/anonymous/`) or use
`mimirtool backfill`. Then backtest against it:

```bash
argus backtest run \
  --rules demo-rules.yaml --incidents demo-history/incidents.yaml \
  --mimir-url … --from 2026-01-01T00:00:00Z --to 2026-01-21T00:00:00Z
```

Every service/error-ratio number is synthetic and the registry says so —
demo output is never presentable as production evidence.
