# CloudOps scenarios with a model (2026-11-05)

The four CloudOps scenarios (`scenarios/README.md`, "CloudOps mesh"), run
once each with a real model on CloudOps's EKS cluster. Their gates and
lifecycle were validated with the stub the same day (B-62); this is the
first time an agent had to diagnose them. One pass, so it is a data point
for B-56, not a result.

## Setup

| | |
|---|---|
| Environment | CloudOps on EKS 1.35 (3 × t3.large): Node, Go, Java and Python services, Loki/Tempo/Mimir, the otel-agent collecting the stdout of the services that do not export logs |
| Agent | `gpt-oss:20b` (MXFP4) on the maintainer's Ollama 0.31.1, 32k context, entirely in GPU memory, through `--agent openai` |
| Budget | 200,000 tokens (the flagship's 100k cap ended 7 of 8 of its unanswered runs), 20 tool calls, 30 min per model call |
| Argus | v2.1.0 |

## Results

| Scenario | Score | Category | Named | Tool calls | Tokens | Outcome |
|---|---|---|---|---|---|---|
| latency-pricing | 0 | — | — | 20 | 107,493 | step limit (22) without submitting |
| network-partition-inventory | 0 | broken-trace-propagation | api-gateway | 8 | 77,016 | submitted |
| deploy-regression-ledger | 0 | — | — | 10 | 40,304 | stopped without submitting |
| missing-service-name-payment | 0.25 | missing-service-name ✓ | api-gateway (a decoy) | 20 | 107,632 | submitted |

Mean score 0.06. Reports with full tool logs:
[`results/2026-11-05-cloudops-gpt-oss-20b/`](https://github.com/tamen25/Argus/tree/main/docs/bench/results/2026-11-05-cloudops-gpt-oss-20b).

## What the tool logs show

- **The token budget was not the limit this time.** No run passed 108k of
  its 200k tokens; the two that ran longest stopped at the 20-call cap.
- **The decoys worked as designed.** Both submitted answers named the
  gateway: the service that returns the 504s and 402s, not the one at fault.
  The partition answer also put it in namespace `cloudops-mesh`, the
  scenario's environment name, which exists nowhere in the cluster.
- **Most failed calls were malformed queries.** TraceQL that Tempo
  rejected, a Loki range in 2024, a `start` left empty, and `list_metrics`
  with `{job=""}`, which Mimir refuses ("match[] must contain at least one
  non-empty matcher"). The last two are inputs the tools could absorb
  instead of erroring (B-63).
- The ledger run gave up, saying it saw "no service-graph metrics": CloudOps
  does not run Tempo's metrics generator, so there are none to find there.

## Not shown

- One pass per scenario, one model, no degraded/remediated pair: nothing
  here says how a stronger agent or more passes would do.
- Every gate fired, so each fault was real and visible in the telemetry.
