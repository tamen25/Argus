# Argus Backtest

- Generated: 2026-07-18T09:00:00Z
- Window: 2026-07-18T05:10:00Z → 2026-07-18T05:45:00Z · step 1m0s
- **Coverage: 32m0s of 35m0s (1 segment(s))**

## HighSpanErrorRatio

| Detected | Missed | Unverifiable | False positives | Pages/week* | Flappiness |
|---:|---:|---:|---:|---:|---:|
| 1 | 1 | 1 | 0 | 31.5 | 1.0 |

| Incident | TTD |
|---|---:|
| 2026-07-18-adfailure-baseline-2 | 7m0s |

- **missed**: 2026-07-12-adfailure-toggle-test
- unverifiable (no telemetry coverage): 2026-07-16-adfailure-spike-baseline

## SpanErrorRatioInstant

| Detected | Missed | Unverifiable | False positives | Pages/week* | Flappiness |
|---:|---:|---:|---:|---:|---:|
| 0 | 0 | 0 | 1 | 148.4 | 0.0 |

- false positive: {service="flagd"} fired 2026-07-18T05:44:00Z

*Pages/week extrapolates firing intervals over covered time only.

## Fidelity caveats

- Replay is not re-execution: stepped instant queries differ from live ruler evaluation in staleness, lookback, and alignment (docs/backtest-fidelity.md).
- replay steps instant queries through time — staleness and ruler-alignment semantics differ from live evaluation
- telemetry covers 32m0s of the 35m0s window — verdicts apply to covered segments only
