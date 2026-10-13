# Argus Bench — `degraded` vs `remediated`

Baseline: `degraded`. Treatment: `remediated`. Δ is treatment − baseline.

## Summary

| Agent | Scenarios compared | `degraded` | `remediated` | Δ | Runs answered (`degraded`) | Runs answered (`remediated`) |
|---|---:|---:|---:|---:|---:|---:|
| `gpt-oss:20b` | 3 of 8 | 0.00 | 0.17 | **+0.17** | 4/8 (50%) | 4/8 (50%) |

## `gpt-oss:20b`

| Scenario | `degraded` | `remediated` | Δ |
|---|---:|---:|---:|
| `broken-trace-propagation-payment` | 0.00 ± 0.00 (1/1) | 0.50 ± 0.00 (1/1) | +0.50 |
| `cardinality-explosion-frontend` | 0.00 ± 0.00 (1/1) | 0.00 ± 0.00 (1/1) | +0.00 |
| `deploy-regression-cart` | no diagnosis (0/1) | no diagnosis (0/1) | — |
| `egress-blackhole-frontend` | 0.00 ± 0.00 (1/1) | 0.00 ± 0.00 (1/1) | +0.00 |
| `missing-service-name-payment` | no diagnosis (0/1) | no diagnosis (0/1) | — |
| `network-partition-product-catalog` | no diagnosis (0/1) | 0.00 ± 0.00 (1/1) | — |
| `oomkill-checkout` | 0.00 ± 0.00 (1/1) | no diagnosis (0/1) | — |
| `redis-latency-cart` | no diagnosis (0/1) | no diagnosis (0/1) | — |

Excluded from this agent's means:

- deploy-regression-cart: no diagnosis under `degraded` (1 attempts)
- missing-service-name-payment: no diagnosis under `degraded` (1 attempts)
- network-partition-product-catalog: no diagnosis under `degraded` (1 attempts)
- oomkill-checkout: no diagnosis under `remediated` (1 attempts)
- redis-latency-cart: no diagnosis under `degraded` (1 attempts)

## Method and caveats

- 5 answered run(s) had a submit_diagnosis call rejected (unparseable, a category not offered, or no well-formed citation) and resubmitted. The check never judges correctness. See `submit_rejections` in the run reports.
- 4 run(s) repeated a model call after the endpoint failed with a 5xx (at most 2 per run). The failed calls' tokens are not counted. See `retries` in the run reports.
- 3 answered run(s) submitted their diagnosis only after one reminder to call a tool, having first replied in prose. See `nudges` in the run reports.
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
