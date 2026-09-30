# Argus Bench

- Scenario: `cardinality-explosion-frontend`
- Agent: `qwen3.6-bench`
- Scenario hash: `8fb638d665e5`
- Environment: `kind-argus-golden`
- Fault categories offered: 5 (`cardinality-explosion`, `dependency-latency`, `deploy-regression`, `network-partition`, `oomkill`)
- Seed: 7
- Budget: 35 tool calls / 400000 tokens per run
- Model: `qwen3.6-bench` (36.0B Q4_K_M) served at `http://127.0.0.1:11434/v1/chat/completions`
- Served context: 32768 tokens (architecture supports 262144; served context is what applies)

## Summary

| Score (mean ± sd) | Answered | Entity score | Category match | Uncited | Budget exhausted | Mean tool calls | Mean tokens |
|---:|---:|---:|---:|---:|---:|---:|---:|
| **0.33 ± 0.47** | 3/4 (75%) | 0.83 ± 0.24 | 67% | 33% | 1 | 13.5 | 22500 |

## Runs

| # | Score | Entity | Category | Decoys | Evidence | Normalization | Tool calls | Tool errors | Tokens | Outcome |
|---:|---:|---:|---|---:|---|---|---:|---:|---:|---|
| 0 | 1.00 | 1.00 | yes | 0 | 2 (logs, metrics) | json | 3 | 1 | 14000 | diagnosed |
| 1 | 0.00 | 0.50 | no (`high-latency`) | 1 | 1 (metrics) +1 malformed | llm-judge | 12 | 0 | 18000 | diagnosed |
| 2 | 0.00 | 1.00 | yes | 0 | **none** | json | 4 | 0 | 6000 | diagnosed |
| 3 | — | — | — | — | — | — | 35 | 0 | 52000 | budget exhausted |

Decoys named (plausible-but-wrong entities asserted): Deployment/otel-demo/product-reviews (1/4)

## Tool use

- Run 0: `query_loki` ×1, `query_prometheus` ×2 (1 failed)

## Method and caveats

- Normalization used: **json** — deterministic.
- Normalization used: **llm-judge** — a model mapped free-form agent output into the scored schema; this step is not deterministic.
- 1 of 54 tool calls returned an error to the agent (a rejected query, or a backend that did not answer). Read `tool_log` in the JSON report before attributing a low score to the agent alone.
- Score = (1−w)·entity agreement + w·category match, less a penalty per decoy named, clamped to [0,1]; it is zero if the scenario required cited evidence and none was given. Deterministic and recomputable by hand from the runs table of a run report.
- Evidence is checked for presence and well-formedness, NOT for truth: verifying an observation would mean re-running the agent's queries. A fabricated citation passes this check — cited telemetry is a floor on effort, not proof of correctness.
- An agent's prose is recorded but never scored.
- Runs that produced no diagnosis (agent error or exhausted budget) are counted separately and excluded from the means — they are not scored as zero. Read the means together with the answered rate.
- Budgets bound what an agent may spend; a low score under a tight budget is a budget result, not only a capability result.
