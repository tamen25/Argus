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
  against the scenario's `groundTruth`, plus a category match. The agent's prose
  and self-reported confidence are recorded but **never scored** — an agent
  does not grade its own answer.
- **A failed run is not a zero.** A run that errors or exhausts its budget is
  recorded with the reason and excluded from the means. A crashed run cannot
  quietly drag an agent's average down.
- **Budgets are part of the result.** `--max-tool-calls` and `--max-tokens` are
  enforced per run and printed on the report; an uncapped run says so
  explicitly. A low score under a tight budget is a budget result, not only a
  capability result.

## Agents

| `--agent` | Needs | Notes |
|---|---|---|
| `openai` | `--endpoint`, `--model` | Any OpenAI-compatible chat-completions endpoint |
| `anthropic` | `--model` | Anthropic Messages API (`--endpoint` optional) |
| `shell` | `--shell-command` | Wraps an existing agent (HolmesGPT, K8sGPT) |

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
| `script` (default) | Runs the scenario's `type: script` steps locally |
| `kubectl` | Applies the scenario's `kubectl`/`chaosmesh` manifests with `kubectl`, deleting them again on cleanup (`--inject-namespace`, `--kube-context`) |
| `none` | Injects nothing — score against an environment you set up yourself |

A Chaos Mesh experiment is itself a CRD manifest, so `kubectl` mode covers both
manifest step types. It shells out to `kubectl` rather than embedding a
Kubernetes API client: the fault surface is "apply this manifest, then delete
it", and this is a bench-time tool, not part of the read-only product path.

**Each injector rejects step types it cannot execute** rather than skipping
them, so a scenario is never scored against an environment that was never
faulted. Cleanup deletes every declared manifest — and keeps going past a
failed delete, since stopping at the first error leaves more faults behind than
it removes.

Steady-state detection is not yet wired: an inject step is expected to settle
before the agent is called, and a report produced this way does not claim steady
state was verified.

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
