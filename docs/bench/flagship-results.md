# Flagship result (v1.0): inconclusive

The flagship question (master plan §3.2) is how much an AI agent's diagnosis
accuracy changes between **degraded** and **remediated** telemetry. The v1.0
run measured it for one open-weights agent, and the answer is: **this run
cannot tell.** The agent scored near zero under both conditions, so there was
almost nothing for better telemetry to improve. This page reports the result as
it came out, with the reasons it is inconclusive.

## What was run

| | |
|---|---|
| Date | 2026-10-13 |
| Code | `main` at `cf9ef34` (binary built from the same snapshot) |
| Agent | `gpt-oss:20b` (MXFP4), served entirely from GPU memory by the maintainer's Ollama 0.31.1, 32k context, through the OpenAI-compatible adapter |
| Environment | kind dev cluster, OpenTelemetry demo, env digest `kind-2026-10-13` |
| Scenarios | the 8 shipped scenarios, closed list of 11 fault categories offered |
| Conditions | `degraded` and `remediated` (`scenarios/conditions/`), each proven in the telemetry before any run |
| Repeats | 1 per cell (16 runs) |
| Budgets | 20 tool calls, 100,000 tokens, 8,192 output tokens per turn, 30-minute call timeout |

The run reports are in
[`results/2026-10-13-gpt-oss-20b/reports/`](https://github.com/tamen25/Argus/tree/main/docs/bench/results/2026-10-13-gpt-oss-20b/reports),
and the full comparison, with every caveat, is in
[`comparison.md`](https://github.com/tamen25/Argus/blob/main/docs/bench/results/2026-10-13-gpt-oss-20b/comparison.md).

## Result

| | `degraded` | `remediated` |
|---|---:|---:|
| Runs answered | 4 of 8 | 4 of 8 |
| Mean score over the 3 scenarios answered under both | 0.00 | 0.17 |
| **Δ (remediated − degraded)** | | **+0.17** |

| Scenario | `degraded` | `remediated` |
|---|---:|---:|
| broken-trace-propagation-payment | 0.00 | 0.50 |
| cardinality-explosion-frontend | 0.00 | 0.00 |
| deploy-regression-cart | no diagnosis | no diagnosis |
| egress-blackhole-frontend | 0.00 | 0.00 |
| missing-service-name-payment | no diagnosis | no diagnosis |
| network-partition-product-catalog | no diagnosis | 0.00 |
| oomkill-checkout | 0.00 | no diagnosis |
| redis-latency-cart | no diagnosis | no diagnosis |

## How to read it

- **The +0.17 is one run.** The broken-trace scenario under remediated
  telemetry named the right fault category (0.50, for category without
  entity). Every other answered run scored 0. With one sample per cell, this
  is not evidence of an effect, and it is not presented as one.
- **It is a floor effect.** Out of 8 answered runs, one named the right
  workload (egress-blackhole under degraded, without a well-formed citation,
  so it scored 0) and one named the right category. Better telemetry cannot
  lift a score the agent does not earn under either condition.
- **Half the runs gave no diagnosis.** Five reached the 100,000-token budget
  (100k–106k used) and answered in prose instead of calling
  `submit_diagnosis`. One stopped at 29k tokens with an empty reply, even after
  the reminder. Two submitted arguments the scorer rejects at 104k–116k tokens,
  past the point where submissions are still checked: a truncated field name,
  and a confidence of 95 on a 0–1 scale. Unanswered runs are excluded from the
  means, not scored as zero.
- **The protocol aids were used and are disclosed.** 5 answered runs had a
  submission rejected and resubmitted, 4 runs repeated a call after a 5xx, and
  3 answered after one reminder to call a tool. None of these judges or changes
  a diagnosis; see [Normalization](../cli/bench.md).
- **The degraded condition is Argus's construction**, evidence is checked for
  form and not truth, and Δ carries no significance test: the standing caveats
  in `comparison.md` apply.

## What the attempt to run it found

The flagship took four sessions. Each one found something in the harness that
measured the wrong thing, and each fix is in v1.0:

| Found | Fix |
|---|---|
| A model looping in its reasoning held one turn open for 30 minutes | per-turn output cap (#90) |
| The remediated condition was "proven" with 1 of 17 services covered | 90% coverage required (#91) |
| Models answered in prose and the run ended | one reminder to call a tool (#92) |
| Budget notices read as "do not call `submit_diagnosis`" | reworded; failed runs keep the last reply or rejected submission (#93) |
| A leftover fault failed the next scenario's reset | sweep fixed (#94) |
| One malformed citation discarded a whole diagnosis | citations parsed leniently, counted only if well-formed (#95) |
| A single endpoint 500 ended the run | bounded 5xx retry (#96) |
| Every answered run scored 0 on citation format alone | `submit_diagnosis` checks its arguments and allows a correction (#97) |

Two smaller local models were tried before this one: the 36B `qwen3.6-bench`
was too slow on a 16 GB GPU (DECISIONS.md, 2026-10-13), and `qwen3:14b` scored
at the same floor in a pass made before most of the fixes above (not
published: it ran on a different harness).

## What would make it conclusive

- **A stronger agent.** The question needs an agent that diagnoses some of
  these incidents. The plan's frontier and budget models were cut for v1.0
  (no hosted APIs; DECISIONS.md, 2026-10-09), but any OpenAI-compatible or
  Anthropic endpoint can run the same matrix: see
  [Running the flagship](flagship.md).
- **More repeats.** Add passes with `scripts/bench-matrix.sh --pass N`; the
  comparison pools them.
- **A larger token budget.** Seven of the eight unanswered runs ended at or past
  the 100,000-token cap. Every turn re-sends the conversation, and these models
  are verbose.
