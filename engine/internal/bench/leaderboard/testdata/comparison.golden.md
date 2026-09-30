# Argus Bench — `degraded` vs `remediated`

Baseline: `degraded`. Treatment: `remediated`. Δ is treatment − baseline.

## Summary

| Agent | Scenarios compared | `degraded` | `remediated` | Δ | Runs answered (`degraded`) | Runs answered (`remediated`) |
|---|---:|---:|---:|---:|---:|---:|
| `model-a` | 2 of 4 | 0.38 | 0.88 | **+0.50** | 5/7 (71%) | 6/6 (100%) |
| `model-b` | 2 of 2 | 0.75 | 0.75 | **+0.00** | 3/4 (75%) | 4/4 (100%) |

## `model-a`

| Scenario | `degraded` | `remediated` | Δ |
|---|---:|---:|---:|
| `s1-cardinality` | 0.25 ± 0.25 (2/2) | 1.00 ± 0.00 (2/2) | +0.75 |
| `s2-latency` | no diagnosis (0/2) | 0.50 ± 0.00 (2/2) | — |
| `s3-oomkill` | 0.50 ± 0.00 (1/1) | · | — |
| `s4-partition` | 0.50 ± 0.00 (2/2) | 0.75 ± 0.25 (2/2) | +0.25 |

Excluded from this agent's means:

- s2-latency: no diagnosis under `degraded` (2 attempts)
- s3-oomkill: not run under `remediated`

## `model-b`

| Scenario | `degraded` | `remediated` | Δ |
|---|---:|---:|---:|
| `s1-cardinality` | 0.50 ± 0.00 (2/2) | 0.50 ± 0.00 (2/2) | +0.00 |
| `s4-partition` | 1.00 ± 0.00 (1/2) | 1.00 ± 0.00 (2/2) | +0.00 |

## Method and caveats

- Δ is treatment − baseline, over scenarios the agent answered under both conditions. A scenario missing or unanswered on either side is listed as excluded, not counted as zero.
- No significance test is applied. Compare Δ with the ± spread and the number of runs before reading it as an effect.
- A cell pools every run of that agent on that scenario under that condition; its mean and ± spread are over answered runs only. Runs that produced no diagnosis are counted in the answered figures and are not scored as zero.
- An agent's mean is the mean of its per-scenario means over the scenarios it answered, so each scenario weighs the same whatever its number of repeats. Read it together with how many scenarios that covers.
- The telemetry condition is a label the operator attached to each run. Argus records it; it does not verify what state the environment was in.
- Score = (1−w)·entity agreement + w·category match, less a penalty per decoy named, clamped to [0,1]; it is zero if the scenario required cited evidence and none was given. Deterministic and recomputable by hand from the runs table of a run report.
- Evidence is checked for presence and well-formedness, NOT for truth: verifying an observation would mean re-running the agent's queries. A fabricated citation passes this check — cited telemetry is a floor on effort, not proof of correctness.
- An agent's prose is recorded but never scored.
- Runs that produced no diagnosis (agent error or exhausted budget) are counted separately and excluded from the means — they are not scored as zero. Read the means together with the answered rate.
- Budgets bound what an agent may spend; a low score under a tight budget is a budget result, not only a capability result.
