# Argus Bench — leaderboard

## Telemetry condition: `degraded`

| # | Agent | Mean score | Scenarios answered | Runs answered | `s1-cardinality` | `s2-latency` | `s3-oomkill` | `s4-partition` |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | `model-b` | **0.75** | 2/2 | 3/4 (75%) | 0.50 ± 0.00 (2/2) | · | · | 1.00 ± 0.00 (1/2) |
| 2 | `model-a` | **0.42** | 3/4 | 5/7 (71%) | 0.25 ± 0.25 (2/2) | no diagnosis (0/2) | 0.50 ± 0.00 (1/1) | 0.50 ± 0.00 (2/2) |

## Telemetry condition: `remediated`

| # | Agent | Mean score | Scenarios answered | Runs answered | `s1-cardinality` | `s2-latency` | `s4-partition` |
|---:|---|---:|---:|---:|---:|---:|---:|
| 1 | `model-a` | **0.75** | 3/3 | 6/6 (100%) | 1.00 ± 0.00 (2/2) | 0.50 ± 0.00 (2/2) | 0.75 ± 0.25 (2/2) |
| 2 | `model-b` | **0.75** | 2/2 | 4/4 (100%) | 0.50 ± 0.00 (2/2) | · | 1.00 ± 0.00 (2/2) |

A cell is mean score ± spread (answered runs/attempts). `no diagnosis` means every attempt failed or ran out of budget; `·` means not run.

## Method and caveats

- A cell pools every run of that agent on that scenario under that condition; its mean and ± spread are over answered runs only. Runs that produced no diagnosis are counted in the answered figures and are not scored as zero.
- An agent's mean is the mean of its per-scenario means over the scenarios it answered, so each scenario weighs the same whatever its number of repeats. Read it together with how many scenarios that covers.
- The telemetry condition is a label the operator attached to each run. Argus records it; it does not verify what state the environment was in.
- Score = (1−w)·entity agreement + w·category match, less a penalty per decoy named, clamped to [0,1]; it is zero if the scenario required cited evidence and none was given. Deterministic and recomputable by hand from the runs table of a run report.
- Evidence is checked for presence and well-formedness, NOT for truth: verifying an observation would mean re-running the agent's queries. A fabricated citation passes this check — cited telemetry is a floor on effort, not proof of correctness.
- An agent's prose is recorded but never scored.
- Runs that produced no diagnosis (agent error or exhausted budget) are counted separately and excluded from the means — they are not scored as zero. Read the means together with the answered rate.
- Budgets bound what an agent may spend; a low score under a tight budget is a budget result, not only a capability result.
