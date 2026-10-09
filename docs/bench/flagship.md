# Running the flagship benchmark

The flagship result (master plan §3.2) is one number per agent: how much its
diagnosis accuracy changes between **degraded** and **remediated** telemetry.
This page is the run book, from an empty cluster to the comparison.

## 1. Prerequisites

- The dev cluster, warm: `make dev-up`, then let the demo serve traffic for at
  least 15 minutes (request metrics only exist once services have handled
  requests).
- `argus` built for WSL and on `PATH`:
  `go build -o ~/go/bin/argus ./engine/cmd/argus`.
- An endpoint for each agent. Argus does not host models: an agent is any
  OpenAI-compatible or Anthropic endpoint (a hosted API, or a server you run
  yourself), or a shell-wrapped agent such as HolmesGPT. Export each API key in
  the shell that runs the matrix.

## 2. Describe the agents

One line per agent, `label | argus bench run flags`:

```text
# agents.txt
claude-sonnet | --agent anthropic --model claude-sonnet-5-5 --api-key-env ANTHROPIC_API_KEY
gpt-budget    | --agent openai --endpoint https://api.openai.com/v1/chat/completions --model <budget-model> --api-key-env OPENAI_API_KEY
holmesgpt     | --agent shell --shell-command holmes --shell-arg ask
```

The plan asks for at least one frontier model, one budget model and HolmesGPT.
The v1.0 flagship ran one open-weights model on a server the maintainer runs
(DECISIONS.md, 2026-10-09), for example:

```text
qwen3.6-bench | --agent openai --endpoint http://<host>:11434/v1/chat/completions --model qwen3.6-bench
```

From WSL, `<host>` is the Windows host as WSL sees it (the default gateway,
`ip route | awk '/^default/{print $3}'`), and the server must listen on that
interface, not only on localhost.

## 3. Read the projection, then confirm

```bash
scripts/bench-matrix.sh --agents agents.txt --out runs/flagship
```

prints the plan and the projected cost and runs nothing. For 8 scenarios × 3
agents × 2 conditions × 3 repeats (144 runs) it projects about 9–14 hours of
wall clock and 7–15M tokens (2–5M per agent), from what the first real runs
measured. Multiply each agent's tokens by its provider's price. Master plan
§12.4 requires this projection to be confirmed before the first full run.

## 4. Run it

```bash
scripts/bench-matrix.sh --agents agents.txt --out runs/flagship --yes
```

For each condition the script applies it with
`scenarios/conditions/apply.sh` (which proves it took effect in the telemetry),
then runs every agent on every scenario with the budgets capped. A run that
fails is recorded in its report and the matrix carries on. A report that already
exists is skipped, so an interrupted matrix resumes where it stopped. The
cluster is returned to the baseline condition at the end, even on failure.

Outputs in `runs/flagship/`:

| File | What it is |
|---|---|
| `<condition>__<agent>__<scenario>.json` | one run report per cell, with its tool logs |
| `<condition>__<agent>__<scenario>.log` | that invocation's stderr |
| `comparison.md`, `comparison.json` | the degraded-vs-remediated comparison |

## 5. Publish

- Load the reports into the Grafana app's **Bench** page: on the dev cluster,
  `kubectl -n argus create configmap argus-bench-reports --from-file=runs/flagship --dry-run=client -o yaml | kubectl apply -f -`,
  then `kubectl -n argus rollout restart deploy/argus-engine`.
- The comparison is the headline. Publish it with its caveats, never without:
  the degraded condition is Argus's construction (see
  `scenarios/conditions/README.md`), evidence is checked for form and not
  truth, and Δ is not a significance test.
