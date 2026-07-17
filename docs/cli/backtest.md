# argus backtest

> **Fidelity-spike harness.** `backtest replay` exists to run the Phase 3
> spike's experiments (see [Backtest fidelity](../backtest-fidelity.md)); the
> full engine — incident registry scoring, TTD, pages/week, `backtest diff` —
> lands across Phase 3. Interfaces may still change.

## `argus backtest replay`

Steps alert rules through a historical window against Mimir, reconstructing
per-series `for:` state the way a live ruler would have evolved it, and
reports would-have-fired intervals.

```bash
argus backtest replay \
  --rules rules/alerts.yaml \
  --mimir-url http://mimir-gateway.lgtm.svc \
  --mimir-tenant anonymous \
  --from 2026-07-16T21:10:00Z --to 2026-07-16T21:35:00Z \
  --step 1m
```

Rule files: Prometheus/Mimir ruler format (`groups:` wrapper) and Mimir's
ruler-API bare-group format (what `mimirtool rules print` emits) both load;
Sloth/Pyrra output is the former. The loader is strict — a file that does not
parse is an error, never a silently empty rule set.

| Flag | Meaning |
|---|---|
| `--rules` | rule file(s), repeatable |
| `--mimir-url` / `--mimir-tenant` | instant-query API endpoint and `X-Scope-OrgID` |
| `--from` / `--to` | window, RFC3339 |
| `--step` | evaluation step; match the live group's `interval` for closest fidelity |
| `--probe-expr` | presence probe for coverage mapping (default `count(target_info)`) |
| `--synthesize` | inline recording rules defined in the loaded set — for history where they never ran |

## Replay is not re-execution

Every report ends with the **fidelity caveats** that applied — coverage
(telemetry presence segments vs the calendar window), synthesized recording
rules, external recording-rule dependencies, and the standing
staleness/lookback divergence caveat. `--synthesize` refuses unsound rewrites
(external recording rules, label-matcher pushdown, recording rules inside
range selectors) instead of guessing.

Presence mapping costs one instant query per step; a 6-day window at 1h
probing stride is ~145 queries. Replay adds one query per alert rule per
covered step.
