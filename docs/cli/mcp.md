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
| `query_prometheus` | Mimir (Prometheus API) | `--mimir-url` |
| `list_alerts` | Mimir (Prometheus API) | `--mimir-url` |
| `query_loki` | Loki | `--loki-url` |
| `search_traces` | Tempo | `--tempo-url` |

`query_prometheus` is an instant query by default; supply `start`, `end`, and
`step` for a range query. `list_alerts` accepts an optional `state` (e.g.
`firing`), filtered client-side so the argument is honored rather than ignored.

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
