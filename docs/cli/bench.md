# argus bench

The fault-injection benchmark (Module C — *Prove*): can an AI SRE agent
diagnose an incident from **your** telemetry? Argus injects a labeled fault,
hands the agent an incident brief plus the read-only
[MCP tool surface](mcp.md), normalizes its answer, and scores it against ground
truth — repeated for variance.

```bash
argus bench run \
  --scenario scenarios/cardinality-explosion-checkout.yaml \
  --agent openai --endpoint https://api.example/v1/chat/completions \
  --model my-model --api-key-env MY_API_KEY \
  --mimir-url http://mimir-gateway.lgtm.svc \
  --repeats 3 --max-tool-calls 20 --max-tokens 100000 \
  --env-digest kind-2026-07-20
```

## What is (and is not) measured

- **The agent is never told the answer.** The brief names only the environment;
  a test asserts it leaks neither the ground-truth entities nor the category.
- **Scoring is deterministic.** Entity-set agreement (Jaccard, or exact match)
  against the scenario's `groundTruth`, combined with the fault category and
  reduced by any decoys named. The agent's prose and self-reported confidence
  are recorded but **never scored** — an agent does not grade its own answer.
- **A failed run is not a zero.** A run that errors or exhausts its budget is
  recorded with the reason and excluded from the means. A crashed run cannot
  quietly drag an agent's average down.
- **Budgets are part of the result.** `--max-tool-calls` and `--max-tokens` are
  enforced per run and printed on the report; an uncapped run says so
  explicitly. A low score under a tight budget is a budget result, not only a
  capability result.

## How a diagnosis is scored

```
score = (1−w)·entity agreement  +  w·category match  −  penalty·decoys named
```

clamped to `[0,1]`, and **zero** if the scenario sets `requireEvidence` and the
agent cited none. `w` is `spec.scoring.categoryWeight` (default 0.5) and
`penalty` is `spec.scoring.decoyPenalty` (default 0.25). The arithmetic is
deliberately simple: a headline number you cannot recompute by hand from the
report is not defensible.

Three things make the difference between grading a diagnosis and grading a
lucky guess:

- **Category is folded in, not filed beside.** Naming the right workload for the
  wrong reason is a partial answer.
- **Evidence can be mandatory.** A diagnosis may cite telemetry
  (`signal`/`query`/`observation`); with `requireEvidence: true` an uncited
  answer scores zero. The scorer checks citations are present and well-formed,
  **never that they are true** — verifying an observation would mean re-running
  the agent's queries. A fabricated citation passes. Cited telemetry is a floor
  on effort, not proof of correctness, and every report says so.
- **Decoys are punished.** `groundTruth.decoys` lists plausible-but-wrong
  entities a naive agent reaches for — the busiest service, or one showing
  correlated symptoms it did not cause. Naming one costs more than the dilution
  it already causes, because a confident wrong attribution is what sends a human
  to the wrong dashboard at 3am. The loader rejects a decoy that is also ground
  truth.

Read `mean_score` together with the **answered rate**: the means average over
runs that produced a diagnosis, so an agent that mostly declines to answer and
guesses well once would otherwise look flawless.

### Calibrating a rubric before you trust it

`--agent=stub` answers without a model or network, so its score is a pure
property of the rubric. Run it against a new scenario before pointing real
agents at it:

```bash
argus bench run --scenario scenarios/my-scenario.yaml \
  --agent stub --stub-profile obvious --stub-obvious <ground-truth-name> \
  --inject none --repeats 3
```

| `--stub-profile` | Answers with | Needs | Should score |
|---|---|---|---|
| `vague` | prose, no entities | — | no diagnosis at all |
| `obvious` | the named workload, generic category, no evidence | `--stub-obvious` | **0.00** |
| `shotgun` | the named workload plus others | `--stub-obvious`, optionally `--stub-shotgun` per decoy | **0.00** (decoys + dilution) |
| `cited` | right entity, right category, **fabricated** evidence | `--stub-obvious`, `--stub-category` | **1.00** — the honest ceiling |

There are no defaults for the entity or category. A calibration that guesses
them measures the guess, not the rubric: with a wrong default entity, `cited`
scores 0.5 instead of 1.0 and understates what fabricated evidence gets away
with. Pass the scenario's own ground truth, and its decoys to `--stub-shotgun`
so the decoy penalty is actually exercised.

If `obvious` scores above zero, the rubric is too loose. If `cited` cannot reach
1.00, it is too tight. `cited` scoring 1.00 is expected and documents the limit
of what form-checking evidence can achieve.

### Repeats start from a clean baseline

Before each repeat injects, `bench run` waits until the scenario's steadyState
signature is observably **absent**, and fails the repeat with a `baseline`
error if it never is. A fault's effects can outlive its cleanup — scenario 1's
frontend keeps exporting every series the fault created, because its OTel SDK
holds cumulative state — and without this check a later repeat would pass its
gate on the previous repeat's residue and be scored on stale telemetry. A
baseline failure means the scenario's reset has to remove the fault's effects,
not only the fault.

## Agents

| `--agent` | Needs | Notes |
|---|---|---|
| `openai` | `--endpoint`, `--model` | Any OpenAI-compatible chat-completions endpoint |
| `anthropic` | `--model` | Anthropic Messages API (`--endpoint` optional) |
| `shell` | `--shell-command` | Wraps an existing agent (HolmesGPT, K8sGPT) |
| `stub` | `--stub-profile` | Calibration instrument, not a bench subject |

## Local inference and the context guard

Any OpenAI-compatible or Anthropic endpoint can be benchmarked. Pass
**`--local-only`** when a run must be guaranteed never to reach a paid API:
endpoints must then be loopback and API keys are refused outright, so the
guarantee is enforced rather than intended. Hostnames other than the loopback
literals are refused *without resolving them* — a name that resolves to
`127.0.0.1` today can resolve elsewhere tomorrow.

For a loopback endpoint, `bench run` asks Ollama what it will actually serve and
**aborts below `--min-context`** (default 32768). This applies whether or not
`--local-only` is set — silent truncation is a property of a locally served
model, not of the billing policy:

```bash
ollama create qwen3.6-bench -f deploy/ollama/Modelfile.qwen3.6-bench
```

This matters more than it looks. `ollama pull qwen3.6:35b-a3b-q4_K_M` yields a
model whose *architecture* supports 262144 tokens but which carries **no
`num_ctx` parameter**, so Ollama serves it at its own small default. The MCP
surface is five tools plus schemas, and each turn appends telemetry to a growing
transcript; the model then drops the earliest tool output with no error. The
agent fails for reasons unrelated to its diagnostic ability, and the result
looks like a finding. Reports print served context beside the architectural
maximum so the flattering number cannot stand alone.

The **judge model must differ from the agent model**, whether or not
`--local-only` is set: a model that misreads its own output the same way twice
launders that error into the score. Tags are compared first; for local models the
**weights** are compared too, because `ollama create` produces a new tag over the
same weights — `qwen3.6-bench` and the `qwen3.6:35b-a3b-q4_K_M` it was built
from have different tags and different manifest digests but one weights blob,
and are refused as a pair.

### Fitting a model that is bigger than your VRAM

If the model does not fit the card, pin the GPU layer count rather than letting
Ollama size it. Automatic sizing over-commits and dies with `CUDA error: shared
object initialization failed`, which reads like a driver incompatibility and is
not one — an explicit `PARAMETER num_gpu N` works on the same hardware.

Tune N by measurement, **unloading between runs** (a resident runner from a
previous test makes lower settings look much worse than they are). On a 16.3 GB
card with the ~22 GiB qwen3.6 at Q4_K_M: CPU-only 2.4 tok/s, 26 layers 46 tok/s,
28 layers 49 tok/s, 30 layers **6 tok/s**. That last one is the trap — passing
the VRAM limit reports no error at all, it just collapses below CPU speed. Leave
headroom for whatever else touches the GPU during a long run.

Because model calls are slow under local inference, `--agent-timeout` defaults
to 10 minutes and `--judge-timeout` to 5 (judging is one short request). Too
short a timeout kills a run mid-investigation and records it as an agent failure
when it was a limit of the machine.

Model tag, quantization, parameter size, served context and endpoint are
recorded on every report — the same tag at a different quantization or context
is a different subject, and a leaderboard row without that cannot be reproduced.

API agents get the identical MCP tool set, so the benchmark compares **agents,
not tool access**. Shell agents bring their own tooling and their token/tool
budgets are **not enforceable** — only a wall-clock timeout applies, and the
report shows their unknown usage dimensions as zero rather than guessing.

## Normalization (and when a model is involved)

Agents answer by calling a synthetic `submit_diagnosis` tool, so structured
output falls out of function-calling and the deterministic JSON normalizer
handles it. For shell agents whose native output we do not control, pass
`--judge-endpoint`/`--judge-model` to enable the **LLM judge** fallback.

The method actually used is recorded per run, and the report names it — if any
run needed the judge, the report says so and flags it as non-deterministic. Runs
that never needed it never mention it.

## Injection

| `--inject` | Behavior |
|---|---|
| `auto` (default) | Manifest steps (`kubectl`, `chaosmesh`) via `kubectl`; `script` steps and the scenario's own `reset`/`cleanup` hooks via `bash` |
| `script` | Legacy: script steps only |
| `kubectl` | Legacy: manifest steps only |
| `none` | Injects nothing — score against an environment you set up yourself |

`--inject-namespace` and `--kube-context` apply to manifest steps and are passed
to scripts as `ARGUS_NAMESPACE` and `ARGUS_KUBE_CONTEXT`, so both target the
same cluster. `kubectl` is shelled out to rather than embedded: the fault surface
is "apply this manifest, then delete it", and this is a bench-time tool, not part
of the read-only product path.

**Each injector rejects step types it cannot execute** rather than skipping
them, so a scenario is never scored against an environment that was never
faulted. For the same reason the legacy modes **refuse** a scenario that
declares hooks instead of skipping its restores.

### Scenario hooks: undoing a fault's effects

Deleting a fault's objects is not always enough to undo it. Scenario 1's load
driver can be deleted, but frontend's OTel SDK keeps exporting every series the
driver created until frontend restarts. A scenario therefore owns its restores:

```yaml
spec:
  reset:                               # before every repeat, after the fault's objects are deleted
    - script: faults/restart-frontend.sh
  cleanup:                             # after every repeat, even a failed one
    - script: faults/restart-frontend.sh
```

**Reset** deletes the fault's objects first, then runs its hooks — stop the
source before cleaning up after it. **Cleanup** runs the hooks, then deletes the
objects, and keeps going past failures, since a half-finished cleanup leaves more
of the fault behind than it removes. Hooks are scripts only: restoring state is
procedural, and a manifest in a cleanup list would be ambiguous (apply it, or
delete it?).

### Steady state and baselines

A scenario's `steadyState` query gates the agent: nothing is asked until the
fault's signature has held for the settle window. Before each repeat injects,
the same signature must be observably **absent** (see "Repeats start from a
clean baseline" above). Prefer a query that forgets a series once it stops being
written — `count(count_over_time(x[2m]))` rather than `count(x)` — or every
repeat waits out Prometheus's 5-minute lookback: measured on scenario 1, the
baseline clears in 121 s with the first form and 302 s with the second.

With `--agent=stub` and no `--mimir-url`, the gate is skipped so a rubric can be
calibrated without a cluster. Given `--mimir-url`, the stub takes the full
lifecycle a real agent does, which validates a scenario's mechanics end to end
at no model cost.

## Output

`--format md` (default) renders a human report; `--format json` is CI-friendly.
Every report carries the reproducibility record — scenario hash, agent, env
digest, seed, budget — plus the standing caveats, which cannot be stripped from
a rendering.

## Importing ITBench scenarios

```bash
argus bench import-itbench --in path/to/ITBench/scenarios/sre/library/indexes/scenarios --out scenarios/itbench
```

Converts [ITBench](https://github.com/itbench-hub/ITBench) SRE scenario index
files (Apache-2.0) so results are comparable with published ITBench baselines.

Imported scenarios are **score-only**, and this is a real constraint rather than
a limitation we glossed: ITBench executes its faults with its own tooling
against a fixed fault catalogue, and Argus cannot reproduce those injections.
Claiming otherwise would make the comparability claim false. So stage the
environment with ITBench, then score the agent with `--inject=none`. The emitted
inject step names a script Argus deliberately **cannot** execute, so running an
imported scenario any other way fails loudly instead of quietly measuring an
un-faulted environment.

Ground truth is derived from each injection's `args.kubernetesObject` — the
object the fault was applied to. Waiter objects (workloads that get restarted or
rescaled to settle the environment) are **collateral, never root cause**. A
scenario whose ground truth cannot be derived is **refused**, not emitted with an
empty answer key that would score every agent answer wrong; pass
`--skip-invalid` to continue past such scenarios instead of failing the import.

Each imported file records its provenance (`metadata.source`, e.g.
`itbench:sre/102`) so a published comparison traces back to the upstream
definition.

## Scenarios

See `scenarios/*.yaml` (schema: `argus/v1alpha1`, `kind: BenchScenario`). The
loader is strict — unknown keys, a bad envelope, an empty inject list, or a
ground truth with no entities are all errors, because a silently dropped
scenario becomes a silently smaller (and wrong) run matrix.
