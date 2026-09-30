# Argus Bench

- Scenario: `cardinality-explosion-frontend`
- Agent: `qwen3.6-bench`
- Scenario hash: `8fb638d665e5`
- Environment: `kind-argus-golden`
- Seed: 7
- Budget: 35 tool calls / 400000 tokens per run
- Model: `qwen3.6-bench` (36.0B Q4_K_M) served at `http://127.0.0.1:11434/v1/chat/completions`
- Served context: 32768 tokens (architecture supports 262144; served context is what applies)

## Summary

| Score (mean ± sd) | Answered | Entity score | Category match | Uncited | Budget exhausted | Mean tool calls | Mean tokens |
|---:|---:|---:|---:|---:|---:|---:|---:|
| **0.33 ± 0.47** | 3/4 (75%) | 0.83 ± 0.24 | 67% | 33% | 1 | 15.0 | 22500 |

## Runs

| # | Score | Entity | Category | Decoys | Evidence | Normalization | Tool calls | Tokens | Outcome |
|---:|---:|---:|---|---:|---|---|---:|---:|---|
| 0 | 1.00 | 1.00 | yes | 0 | 2 (logs, metrics) | json | 9 | 14000 | diagnosed |
| 1 | 0.00 | 0.50 | no | 1 | 1 (metrics) +1 malformed | llm-judge | 12 | 18000 | diagnosed |
| 2 | 0.00 | 1.00 | yes | 0 | **none** | json | 4 | 6000 | diagnosed |
| 3 | — | — | — | — | — | — | 35 | 52000 | budget exhausted |

Decoys named (plausible-but-wrong entities asserted): Deployment/otel-demo/product-reviews (1/4)

## Method and caveats

- Normalization used: **json** — deterministic.
- Normalization used: **llm-judge** — a model mapped free-form agent output into the scored schema; this step is not deterministic.
- Score = (1−w)·entity agreement + w·category match, less a penalty per decoy named, clamped to [0,1]; it is zero if the scenario required cited evidence and none was given. Deterministic and recomputable by hand from this table.
- Evidence is checked for presence and well-formedness, NOT for truth: verifying an observation would mean re-running the agent's queries. A fabricated citation passes this check — cited telemetry is a floor on effort, not proof of correctness.
- An agent's prose is recorded but never scored.
- Runs that produced no diagnosis (agent error or exhausted budget) are counted separately and excluded from the means — they are not scored as zero. Read the means together with the answered rate.
- Budgets bound what an agent may spend; a low score under a tight budget is a budget result, not only a capability result.
