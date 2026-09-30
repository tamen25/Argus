# The first real-model run (deploy-regression-cart)

A record of the first bench runs against a real model, on 2026-09-30, and what
they showed about the harness. Every run before this used the calibration stub,
which answers without calling a tool. The stub validates a scenario's mechanics
and its rubric; it cannot show what an agent experiences.

**Setup.** Scenario `deploy-regression-cart` (hash `1f93bb0389c4`), on the kind
dev cluster. Subject: `qwen3.6-bench` (36B, Q4_K_M, served context 32768) through
Ollama's OpenAI-compatible endpoint on loopback, with `--local-only`. Budget: 20
tool calls, 100000 tokens. Tools: `query_prometheus`, `query_loki`,
`search_traces`, `list_alerts`.

## Run 1: no diagnosis

| Wall clock | Tool calls | Tokens | Outcome |
|---:|---:|---:|---|
| 333 s | 20 | 67337 | budget exhausted |

The lifecycle worked: reset, clean baseline, inject, gate (frontend 5xx at
0.47/s), agent, cleanup. The model investigated for 20 tool calls and was cut
off on the 21st.

**Finding 1: the agent was never told it had a budget.** The cap was enforced and
printed on the report, and nothing in the brief mentioned it. A limit the
subject cannot see measures whether it happens to stop early. The brief now
states the caps, and when the last permitted tool call is used the agent is told
and gets one turn to submit.

**Finding 2: the report could not say what the agent did.** It recorded 20 tool
calls and nothing about them, so a model that investigated badly looked the same
as one whose tools were failing. Run reports now carry a tool log.

## Run 2: a scored diagnosis

| Wall clock | Tool calls | Tool errors | Tokens | Score | Entity | Category |
|---:|---:|---:|---:|---:|---:|---|
| 265 s | 20 | 6 | 89710 | **0.00** | 0.00 | no (`PerformanceDegradation`) |

With the budget stated, the model used its 20 calls and submitted on the final
turn. It named `Deployment/otel-demo/flagd`; the root cause was
`Deployment/otel-demo/cart`. The score is a fair zero for this answer. The tool
log shows how it got there, and three more things about the harness:

| Tool | Calls | What happened |
|---|---:|---|
| `query_prometheus` | 10 | 8 returned an empty result, 2 were rejected as invalid PromQL |
| `search_traces` | 6 | 3 rejected as invalid TraceQL |
| `query_loki` | 3 | 1 rejected as invalid LogQL |
| `list_alerts` | 1 | answered |

**Finding 3: the agent cannot discover what telemetry exists.** Every PromQL
query that parsed filtered on `namespace="otel-demo"` and used a guessed metric
name (`http_requests_total`, `grpc_errors_total`, `kube_pod_status_phase`). None
of those exist here: services are labeled `job`, and the request metric is
`app_frontend_requests_total`. The tool surface has no way to list metric names
or label values, and the tool descriptions say only "run a PromQL query". Eight
of twenty calls bought nothing. A stronger agent might probe with
`count by (__name__) ({job!=""})`; this one did not, and the harness gave it no
reason to.

**Finding 4: the fault category has no vocabulary.** The agent answered
`PerformanceDegradation`. The rubric compares the category to the scenario's own
slug (`deploy-regression`) and gives it half the score, and the agent is never
shown the list of categories. No agent can match a slug it has not seen, so as
things stand 0.50 is the practical ceiling for a correct diagnosis. The `cited`
calibration stub reaches 1.00 only because it is handed the slug.

**Finding 5: `get_k8s_topology` is not offered.** The tool exists in the MCP
server, and `bench run` does not wire a topology backend. The agent is asked to
name Kubernetes entities and has no tool that lists them.

## What this means for results

Findings 1 and 2 are fixed. Findings 3 to 5 are open (BACKLOG B-41, B-42), and
until they are decided, a bench score measures the harness as much as the agent:
the category half of the rubric is unreachable, and the entity half depends on
the agent guessing this environment's label scheme. No leaderboard or
degraded-vs-remediated comparison should be published from the current surface.

Both runs left the cluster clean: cart's `VALKEY_ADDR` restored, no annotations,
no fault objects.
