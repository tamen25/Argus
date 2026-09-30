# argus mcp

Serves a **read-only** observability tool surface over the [Model Context
Protocol](https://modelcontextprotocol.io) (JSON-RPC 2.0 on stdio). This is the
same tool surface the Phase 4 bench harness gives every agent, so a benchmark
compares agents rather than tool access — and it is independently useful as an
MCP server for any MCP-capable client (Claude Desktop, IDE agents, etc.).

```bash
argus mcp --mimir-url http://mimir-gateway.lgtm.svc \
          --loki-url  http://loki-gateway.lgtm.svc \
          --tempo-url http://tempo.lgtm.svc \
          --tenant    anonymous
```

## Tools

| Tool | Backend | Enabled by |
|------|---------|-----------|
| `get_k8s_topology` | Kubernetes (`kubectl get`), plus the service graph from Mimir | `--kube-topology` |
| `list_metrics` | Mimir (label API) | `--mimir-url` |
| `list_metric_labels` | Mimir (label API) | `--mimir-url` |
| `list_log_labels` | Loki (label API) | `--loki-url` |
| `list_trace_tags` | Tempo (tag search API) | `--tempo-url` |
| `query_prometheus` | Mimir (Prometheus API) | `--mimir-url` |
| `query_loki` | Loki | `--loki-url` |
| `search_traces` | Tempo | `--tempo-url` |
| `list_alerts` | Mimir (Prometheus API) | `--mimir-url` |

`query_prometheus` is an instant query by default; supply `start`, `end`, and
`step` for a range query. `list_alerts` accepts an optional `state` (e.g.
`firing`), filtered client-side so the argument is honored rather than ignored.

### Discovery

Metric names, label names and trace attributes differ between environments, and
a query on a name that does not exist returns an empty result rather than an
error. The `list_*` tools say what exists, the way a metric browser does for a
human:

- `list_metrics` — metric names with samples in the last 15 minutes; optional
  `selector` (e.g. `{job="my-service"}`) and `match` (substring).
- `list_metric_labels` — label names, or with `label`, that label's values.
- `list_log_labels` — Loki stream labels, or one label's values (last hour).
- `list_trace_tags` — Tempo attributes, scoped as TraceQL writes them
  (`resource.service.name`), or one attribute's values.

Each answers `{"total", "returned", "truncated", "values"}`, sorted and capped
at 100 values by default (500 at most): a busy Mimir has thousands of metric
names, and handed over whole they would fill a model's context. `truncated`
says when to narrow the request. These four are the one place the server shapes
an answer; every query tool still returns the backend's JSON unchanged.

The query tools' descriptions point at the discovery tools only when those are
offered, and use neutral examples (`my-service`), never a name from your
environment.

### Topology

`get_k8s_topology` lists the cluster's Deployments, StatefulSets and DaemonSets
by **identity only** — kind, namespace, name, never status — so an agent learns
what the entities are called, not which one is broken. With `--mimir-url` it
adds the trace-derived service graph (`traces_service_graph_request_total`,
caller → callee). It runs `kubectl get` with your current kubeconfig
(`--kube-context` to choose); that is a read, and the only command it issues.

## Read-only by construction

Every tool is a `GET`. The server holds **no write credentials** and there is
no code path that mutates your systems (architecture rule 5) — read-only isn't a
policy here, it's the absence of any write port. Each tool advertises the MCP
`readOnlyHint` annotation.

## Partial, honest surface

Only tools whose backend URL you provide are registered. An MCP client sees a
smaller surface rather than tools that fail at call time; starting with no
backend URL is an error (an empty surface is useless). Tool responses are the
backend's native JSON, passed through unchanged — Argus adds no interpretation
on this path.

## Wiring an MCP client

Most clients launch an MCP server as a subprocess. Example client config:

```json
{
  "mcpServers": {
    "argus": {
      "command": "argus",
      "args": ["mcp", "--mimir-url", "http://localhost:9009", "--tenant", "anonymous"]
    }
  }
}
```

`get_k8s_topology` is part of the designed surface but ships with the bench
Kubernetes adapter in a later Phase 4 slice; until then it is simply absent from
the advertised tools.
