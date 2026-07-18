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

## `argus backtest run`

Replays alert rules over history and **scores them against the incident
registry** (`incidents.yaml`): detections with TTD, missed incidents,
unverifiable incidents (no telemetry coverage — never counted as misses),
false positives (fires outside every incident window ± `--grace`),
pages/week extrapolated over *covered* time, and flappiness (firing
intervals per detected incident). Renders Markdown or JSON (`--output`).

```bash
argus backtest run \
  --rules rules/alerts.yaml \
  --incidents incidents.yaml \
  --slo slo/checkout.yaml \
  --mimir-url http://mimir-gateway.lgtm.svc --mimir-tenant anonymous \
  --from 2026-07-18T05:00:00Z --to 2026-07-18T06:00:00Z
```

`--slo` takes a burn-rate policy file: multi-window burn-rate alert rules
(standard 5m/1h + 30m/6h fast/slow pairs, or custom windows) are generated
from the policy and replayed alongside `--rules`, so an SLO change is
backtested exactly like a rule change.

## `argus backtest diff` — the CI gate

Replays rule set A (current) and rule set B (proposed) over the same covered
window and diffs the set-level verdicts — an incident counts as detected if
*any* rule in the set fired for it:

```bash
argus backtest diff \
  --rules-a rules/current/ --rules-b rules/proposed/ \
  --incidents incidents.yaml \
  --mimir-url … --from … --to … \
  --max-ttd-regression 5m --max-pages-week 50
```

- **Losing a detection always fails.** Gained detections are reported.
- `--max-ttd-regression` fails when any incident's best TTD worsens beyond it.
- `--max-pages-week` fails when set B's total exceeds the budget.
- Exit code is non-zero on regression — wire it straight into CI.
- Reports over different coverage are **refused, not fudged**: both sets are
  replayed in one invocation over identical segments.

## Live endpoint (plugin Backtest page)

`argus serve --backtest-rules alerts.yaml --backtest-mimir-url … --backtest-incidents incidents.yaml`
exposes **`/api/backtest`**, the report JSON the plugin's Backtest page reads
(through its backend proxy). The endpoint replays a rolling
`--backtest-window` (default 7 days) ending now, caches the result for
`--backtest-cache-ttl` (default 15m) so polling never re-runs the replay, and
reloads the rule and incident files each recompute so edits are reflected
without a restart. With no `--backtest-rules` it returns 404 and the page
shows "not configured" rather than an empty report.

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
