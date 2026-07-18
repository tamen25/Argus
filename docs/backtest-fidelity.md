# Backtest fidelity — Phase 3 spike design note

> **Spike charter** (master plan §6.4, mandatory first task of Phase 3):
> characterize the known failure modes of historical alert-rule replay
> *before* building the backtest engine, so its reports state their fidelity
> caveats from day one. Timebox: **2026-07-17 → 2026-07-24**. This note is the
> spike's deliverable and grows as findings land; the engine's report footer
> will always state which caveats applied to a given run.

## Why replay is not re-execution

`argus backtest` answers "would these rules have fired?" by re-evaluating rule
expressions over historical Mimir data. That is **not** the same computation
the ruler ran (or would have run) live, and the differences are exactly the
failure modes below. A backtest that hides them produces confident nonsense —
the one thing Argus never ships (architecture rule 7).

## Failure modes and findings

### (a) Rules that reference recording rules

An alert like `service:span_errors:ratio_rate5m > 0.05` reads a series that
only exists if the recording rule producing it ran historically. Replaying it
against history where the recording rule wasn't loaded finds **no data at
all** — silently, because absent series are not errors in PromQL.

**Approach (implemented in the spike, `engine/internal/backtest/deps.go`):**
static dependency analysis over the parsed expressions of the loaded rule set.
Every vector selector is classified:

| Class | Meaning | Replay treatment |
|---|---|---|
| plain series | scraped/ingested metric | replay directly |
| defined recording | recording rule in the loaded set | **synthesis mode**: evaluate its expression inline |
| external recording | colon-form name, not in the loaded set | cannot synthesize — flag the rule, don't guess |

Synthesis-mode caveats (to quantify during the spike week): inline evaluation
computes the recording rule at query time over raw series, while the live
ruler evaluated it on its own `interval` and *stored* the result — staleness,
evaluation jitter, and subquery-vs-stored semantics can all diverge. Findings
land here.

### (b) `for:` clauses and staleness under replay

A `for: 5m` alert fires only after its condition held for 5 continuous
minutes *of ruler evaluations*. Replay reconstructs that state by stepping
queries through time, which differs from live evaluation in lookback-delta
handling, staleness markers, and evaluation alignment.

**Live baseline started 2026-07-17:** the dev Mimir ruler now runs the spike
rule group (`deploy/kind/mimir-rules/argus-spike.yaml`) — a recording rule,
an alert with `for: 5m`, and an identical twin with `for: 0` — so replayed
firings can be compared against the real `ALERTS`/`ALERTS_FOR_STATE` series
those rules write from today onward. Divergence numbers land here after
enough sessions (and at least one induced fault) accumulate.

**Finding (2026-07-17):** the dev cluster had **zero** live rule history
before today — `ALERTS` was empty, the ruler had no rule groups. Any project
adopting backtest hits the same cold start: you cannot quantify replay
fidelity for `for:` semantics without a live baseline. The engine must say
so rather than claim validated fidelity it cannot have.

**First live measurement (2026-07-16 21:16–21:30 UTC, incident
`2026-07-16-adfailure-spike-baseline`):** with the adFailure fault active,
`SpanErrorRatioInstant` (`for: 0`) went active per service between 21:24:05
and 21:27:05, and `HighSpanErrorRatio` (`for: 5m`) fired **exactly +5m** after
its pending start (accounting: active 21:24:05 → firing 21:29:05). Three
additional live behaviors any replay must reproduce or disclose:

1. **Rule edits reset `for:` clocks.** Reloading the rule group at 21:26:06
   wiped all pending state — ad's pending from ~21:19 restarted at 21:26:05.
   A backtest of a rule set that changed mid-history diverges from live
   unless it replays each rule-file era separately.
2. **Sparse signals flap thresholds.** The fault produced ~2 error spans per
   5m rate window; at thresholds near the signal level (5%, 2%) the alert
   fired and resolved within 92 seconds. Pages/week estimates must model
   this quantization, not assume smooth ratios.
3. **Fault ≠ fault-visible.** The flag flipped at 21:05 but the ad service
   held a stale flagd stream until a pod restart at ~21:16 — ground-truth
   incident windows (incidents.yaml) and telemetry-visible impact windows
   differ, and TTD must be measured against the latter with the former
   disclosed.

**Setback (2026-07-17):** the dev cluster was deleted before the 20:00–22:00Z
block was cut, so the experiment window's telemetry (21:10–21:35Z) — including
the live `ALERTS` baseline — was lost with the WAL (a documented Phase 0
acceptance: unflushed WAL ~2h is lost on recreation). Incident
`2026-07-16-adfailure-spike-baseline` now sits in the registry with **no
surviving telemetry**, making it the canonical *unverifiable* incident the
coverage-before-verdicts design predicted. The live divergence comparison
re-runs on the recreated cluster later in the spike week.

**Replay validated against preserved history (2026-07-17, measured):** a
minimal read-only stack (MinIO + `grafana/mimir` monolithic,
`target=query-frontend,querier,store-gateway` so nothing writes into the
preserved bucket) over the surviving blocks ran
`argus backtest replay --synthesize` for 2026-07-16 14:40–20:00Z — a window
where the spike rules **never ran live**:

- coverage honestly reported: **1h14m of the 5h20m calendar window**, 2 segments;
- the synthesized recording rule found real error episodes (Docker-recovery
  and flagd-restart noise) predating the rules' existence;
- `SpanErrorRatioInstant` (for: 0) fired **28 times**; `HighSpanErrorRatio`
  (for: 5m) fired **once** (accounting, condition sustained 15m) — the `for:`
  clause suppressed 27 of 28 would-be pages on identical data, which is the
  pages/week story `backtest diff` exists to tell.

**Quantified (2026-07-18, incident `2026-07-18-adfailure-baseline-2`):** the
re-run baseline captured live twins AND was replayed over the same window
(direct mode against the stored recording-rule series = pure (b); synthesize
mode = (a) on identical data):

| | instant (`for: 0`) fired | `for: 5m` fired | `for:` delta |
|---|---|---|---|
| live ALERTS (1m samples) | 05:18:00 | 05:23:00 | +5m |
| direct replay | 05:17:00 | 05:22:00 | +5m |
| synthesized replay | 05:18:00 | 05:23:00 | +5m |

- **Replay-vs-live divergence: ±1 evaluation step** (ruler evaluates on its
  own offset; replay steps on aligned minutes). No missed and no extra
  firings across all services in the window, including the post-fault
  flapping.
- **Synthesis-vs-direct: +1 step systematic lag** (stored samples carry the
  ruler's evaluation lookback; inline evaluation computes fresh at query
  time), identical firing sets otherwise.
- `for:` duration is preserved **exactly** in every mode.

**Decision — query stepping, not promql-engine embedding (recorded in
DECISIONS.md):** stepping reproduced live behavior to within one evaluation
interval on a real baseline, and its error mode (step alignment) is simple to
state as a caveat. Embedding the engine would chase sub-step staleness
semantics at the cost of a far larger surface (storage adapters, lookback
internals) for accuracy the caveat already bounds honestly.

### (c) Retention bounds the usable window

Mimir's `compactor_blocks_retention_period` (365d on the dev cluster) bounds
how far back replay can see — but the *actual* bound is the earliest data,
not the configured retention.

**Approach (implemented, `engine/internal/backtest/window.go`):**
`UsableWindow` binary-searches the earliest timestamp with data
(O(log n) instant queries) and the report states the window it actually
covered. Never fail silently on an over-wide `--from`.

### (d) History is holed, not just bounded — measured

The textbook version of this failure mode is series renames/relabels breaking
continuity. The dev cluster showed a stronger version on day one:

**Finding (2026-07-17, measured):** probing `count(target_info)` at 1h stride
over the last 6 calendar days found **36 hours of data in 4–5 discontinuous
sessions** (07-11 23:00–07-12 03:00, 07-14 06:00–21:00, 07-15 08:00–20:00,
07-16 05:00–08:00, …) — the dev machine is only on during work sessions.
Presence is **not monotone**, so "find the earliest sample" is not enough.

**Approach (implemented, `engine/internal/backtest/segments.go`):** `Segments`
sweeps the range at a configurable stride and returns maximal presence runs.
The backtest evaluates **within segments only** and reports coverage — e.g.
"36h evaluated across a 144h calendar window (25%)". A `for:` state that
would span a gap boundary is undefined and the affected rule gets flagged.
Per-series continuity (renames) uses the same primitive with a per-series
matcher; a real disappearance exists in dev history (`flagd-ui` container
removed 2026-07-11, see `incidents.yaml`) to validate against.

## Consequences for the engine design

1. **Fidelity caveats are structured data**, not prose: each replay result
   carries the list of caveats that applied (synthesized recording rules,
   coverage ratio, flagged gap-crossing `for:` states, external deps).
   The report footer renders them; `backtest diff` refuses to compare runs
   whose caveat sets differ materially.
2. **Coverage before verdicts**: a rule set can only be scored against the
   segments both the rules and the incidents actually cover; incidents
   (from `incidents.yaml`) falling outside telemetry segments are reported
   as *unverifiable*, not as misses.
3. **Synthetic history is a first-class need** (master plan §3.2): real dev
   accumulation produces short gappy sessions; demos and CI need the
   `synth-history` generator to exercise multi-week continuous scenarios.
4. Adapters implement two ports (`InstantQuerier` today; a range/matrix port
   for the replay evaluator next) — Mimir concretes stay in an adapter
   package per architecture rule 1.

## Spike status

- [x] Rule-file loading (Prometheus/Mimir ruler format, strict) — `rules.go`
- [x] Recording-rule dependency detection — `deps.go`
- [x] Usable-window probe — `window.go`
- [x] Presence-segment mapping + live measurement on dev history — `segments.go`
- [x] Live ruler baseline running for `for:`-divergence data — `deploy/kind/mimir-rules/`
- [x] Replay evaluator prototype (instant-query stepping + for-state tracking) — `replay.go`
- [x] Recording-rule synthesis prototype (inline substitution, unsound cases refused) — `synth.go`
- [x] Mimir adapter + `argus backtest replay` CLI harness; validated end-to-end on preserved real history (28 vs 1 firings above)
- [x] Quantified (a)/(b) divergence vs a live baseline: ±1 evaluation step, `for:` exact, no missed/extra firings (2026-07-18 re-run after the first baseline was lost with the WAL)
- [x] Decision: **query stepping** for v0.3 — accuracy bounded by one step and honestly stated; engine embedding rejected as complexity without commensurate fidelity

**Spike complete 2026-07-18, five days inside the timebox.** The engine work
(incident scoring, burn-rate simulation, `backtest diff`, synth-history,
plugin page) builds on these primitives with the caveat structure above.
