# The first real-model run (deploy-regression-cart)

A record of the first bench runs against a real model, on 2026-09-30, and
what they showed about the harness. Every run before this used the calibration stub,
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

## Run 3: with the category list

| Wall clock | Tool calls | Tool errors | Tokens | Score | Entity | Category |
|---:|---:|---:|---:|---:|---:|---|
| 223 s | 20 | 9 | 48865 | **0.00** | 0.00 | no (`broken-trace-propagation`) |

Finding 4 is fixed: every agent is now offered the closed category list, as an
enum on `submit_diagnosis` and in the brief. The model answered with a category
from the list. It was the wrong one, which is a result about the agent and no
longer one about the harness.

The rest of the run confirms findings 3 and 5. The model filtered on
`environment="otel-demo"` this time, another label that does not exist, and all
eight PromQL queries that parsed returned nothing. Seven of eight trace
searches were rejected as invalid TraceQL. And it named the root cause as
`{kind: "service", namespace: "", name: "flagd"}`: with no tool that lists the
cluster's workloads, it had no way to know the entities are `Deployment`s in
`otel-demo`, so even the right service name would have scored zero.

## Run 4: with discovery tools and topology

| Wall clock | Tool calls | Tool errors | Tokens | Outcome |
|---:|---:|---:|---:|---|
| 309 s | 17 | 2 | 109404 | token budget exhausted |

Findings 3 and 5 are fixed: the surface now has `list_metrics`,
`list_metric_labels`, `list_log_labels`, `list_trace_tags` and
`get_k8s_topology`. The investigation changed completely. The model called
`get_k8s_topology` first, then found real metric names (a span-error recording
rule, a database latency histogram) and queried them. It corrected its TraceQL
after one rejected query, using the syntax example in the tool description.
15 of 17 calls succeeded, against 6 of 20 in run 2.

(An earlier attempt at this run failed on its first model call: Ollama's
`llama-server` could not initialize CUDA. Restarting Ollama fixed it. The
report recorded the failure as an agent error with no diagnosis, as it should.)

**Finding 6: the token budget has no warning.** The run ended on the token cap
(109404 of 100000) with three tool calls to spare. Every turn re-sends the whole
conversation, so the count grows faster than the agent can track, and unlike the
tool-call cap there was no notice and no final turn. The report also did not say
which cap was hit.

## What this means for results

Findings 1 to 5 are fixed. Finding 6 is open (BACKLOG B-43). Until it is, a run
can still end on a budget the agent could not see coming, so no leaderboard or
degraded-vs-remediated comparison should be published yet.

All runs left the cluster clean: cart's `VALKEY_ADDR` restored, no
annotations, no fault objects.
