# Making the thresholds yours

Argus ships with defaults for every numeric rule threshold — and several of
them are honest guesses, flagged as such since the fanout (`SPA-003`'s 200
span names, `ARG-LOG-001`'s 0.5 correlation ratio, `ARG-RES-004`'s 3
resource-attr values). Your fleet is the only authority on what *normal*
looks like. `argus rules calibrate` turns your own telemetry into reviewed,
committed threshold overrides.

## How it works

```bash
# 1. accumulate evidence (a 24h soak is the intended source)
make soak

# 2. propose
argus rules calibrate --soak-dir soak-output/<ts> [--store-dsn postgres://…]

# 3. review the table it prints, then keep what you accept
git add calibrated-rules/ && git commit
argus score --rules ./calibrated-rules ...
```

The command reads every `aggregates-*.json` and `report-*.json` snapshot in
the soak dir (plus, with `--store-dsn`, finding ratios persisted by score
runs), computes **robust statistics only** — median, MAD, nearest-rank
P90/P99; telemetry distributions are heavy-tailed, so means and standard
deviations would be dominated by outliers — and emits one override YAML per
rule with evidence in the header comment.

## Hard boundaries

- **Criteria are never modified.** Calibration adjusts only the params a
  spec rule leaves open (`MET-001`, `SPA-002`, `SPA-003`) and
  argus-extension params. The emitted file is a complete rule copy
  (override-by-ID semantics) with exactly one value changed.
- **Deterministic.** The same snapshot set produces byte-identical output —
  reviewable, diffable, re-runnable.
- **No invented evidence.** Rules whose distribution has zero observations
  get no proposal.
- **Human in the loop.** Argus writes files; you review and commit them.

## Proposal formulas

Declared per rule in its `calibration:` block (see
[rule authoring](rules/authoring.md)); all inputs are the observed
distribution of the named aggregate field or finding ratio:

| Kind | Formula | Used by |
|---|---|---|
| `count` | ceil to 2 significant digits of **P99 × 2** | MET-001, SPA-003 |
| `small_count` | **⌈P99⌉ + 1** | ARG-RES-004 |
| `ratio` | **min(1, P99 + max(0.05, 2×MAD))**, 2 decimals | SPA-002, ARG-SPA-002, ARG-LOG-001 |

The headroom is deliberate: calibration describes *today's* fleet, and a
threshold sitting exactly on P99 would page on the next normal deploy.

## Caveats the output repeats

- `finding_ratio` sources (ARG-LOG-001) see **failing services only** —
  services already passing carry no stats in reports. Aggregate sources
  cover every tracked service.
- Distributions from a sampled mirror are lower bounds for cardinality
  counts; the poller-verified path (`MET-001`) is authoritative where
  available.
