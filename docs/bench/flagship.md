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
holmesgpt     | --agent shell --shell-command holmes --shell-arg ask --shell-arg "what broke?"
```

Flags are split the way a shell splits words, so a quoted argument stays one
argument; nothing in the file is expanded or run. Labels must be unique.

The plan asks for at least one frontier model, one budget model and HolmesGPT.
The v1.0 flagship ran one open-weights model on a server the maintainer runs
(DECISIONS.md, 2026-10-09 and 2026-10-13); its result is in
[Flagship result (v1.0)](flagship-results.md):

```text
gpt-oss-20b | --agent openai --endpoint http://<host>:11434/v1/chat/completions --model gpt-oss:20b --agent-timeout 30m
```

Serve the model entirely from GPU memory if you can. A model partly offloaded to
CPU is several times slower, and late turns of a long investigation can take
more than 10 minutes each.

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

In one sitting:

```bash
scripts/bench-matrix.sh --agents agents.txt --out runs/flagship --yes
```

For each condition the script applies it with
`scenarios/conditions/apply.sh` (which proves it took effect in the telemetry),
then runs every agent on every scenario with the budgets capped. A run that
fails is recorded in its report and the matrix carries on. The cluster is
returned to the baseline condition at the end, even on failure.

### In sessions

The matrix does not have to run in one sitting. A cell whose report exists, and
was made with the same condition, repeats and budget, is skipped, so the same
command resumes where the last one stopped. A report made with other settings
stops the matrix (delete it, or use its flags), and one that is not valid JSON
is set aside as `.invalid` and its cell runs again.
`--stop-after <minutes>` ends a session on time (the cell running then finishes
first). Ctrl-C is safe too: the run in progress removes its fault and writes no
report, so that cell runs again next time.

Repeats can be added in **passes** instead of all at once. Each pass is one
repeat of every cell, and the comparison pools every pass so far. Running one
condition per session saves a condition switch:

```bash
# session 1 and 2: pass 1
scripts/bench-matrix.sh --agents agents.txt --out runs/flagship --repeats 1 --pass 1 --conditions degraded   --stop-after 100 --yes
scripts/bench-matrix.sh --agents agents.txt --out runs/flagship --repeats 1 --pass 1 --conditions remediated --stop-after 100 --yes
# sessions 3 and 4: pass 2, the same with --pass 2
```

The comparison is written as soon as both conditions have reports, and
rewritten after every session. Read its answer rates and its spread before
deciding whether another pass is worth it.

After a Docker or PC restart, run `make dev-heal` and let the demo serve
traffic for about 15 minutes before a session.

Outputs in `runs/flagship/`:

| File | What it is |
|---|---|
| `reports/pass-<N>/<condition>__<agent>__<scenario>.json` | one run report per cell, with its tool logs |
| `logs/pass-<N>/<condition>__<agent>__<scenario>.log` | that invocation's stderr |
| `comparison.md`, `comparison.json` | the degraded-vs-remediated comparison over every pass |

## 5. Publish

- Load the reports into the Grafana app's **Bench** page. On the dev cluster
  (each key prefixed with its pass, since passes reuse file names):

    ```bash
    args=(); for f in runs/flagship/reports/pass-*/*.json; do
      args+=(--from-file="$(basename "$(dirname "$f")")__$(basename "$f")=$f"); done
    kubectl -n argus create configmap argus-bench-reports "${args[@]}" --dry-run=client -o yaml | kubectl apply -f -
    kubectl -n argus rollout restart deploy/argus-engine
    ```

- The comparison is the headline. Publish it with its caveats, never without:
  the degraded condition is Argus's construction (see
  `scenarios/conditions/README.md`), evidence is checked for form and not
  truth, and Δ is not a significance test.
