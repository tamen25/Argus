# Quickstart (dev environment)

This page covers the development environment. To just run the CLI, download an
archive for your platform from the
[GitHub releases](https://github.com/tamen25/Argus/releases) and check it
against `checksums.txt`. The Grafana plugin zip is attached to the same release
(see [Releasing](releasing.md) for what each asset contains).

## Prerequisites

Linux or WSL2 Ubuntu 24.04. Install:

- Go ≥ 1.26 (the `toolchain` line in `engine/go.mod` pins the exact release,
  and `go` downloads it automatically)
- Node 22 + npm
- Docker (or Docker Desktop with WSL2 integration)
- [kind](https://kind.sigs.k8s.io/), kubectl, [helm](https://helm.sh/)
- make

## One-command dev cluster

```bash
make dev-up
```

This creates a kind cluster named `argus` running:

- **Grafana LGTM stack** — Mimir (metrics, **365d retention** for backtest history
  accumulation), Loki (logs), Tempo (traces), Grafana, Alloy (OTLP gateway)
- **OpenTelemetry Demo** — the workload emitting telemetry
- **Chaos Mesh** — fault injection for bench scenarios and incident logging
- **The argus engine** — built from your checkout, in namespace `argus`,
  receiving the sampled telemetry mirror from Alloy. After changing engine code,
  `make dev-engine` rebuilds and redeploys it. Reach its API with
  `kubectl -n argus port-forward svc/argus-engine 8080`, then
  <http://localhost:8080/api/report>.

Grafana: <http://localhost:3000> (admin / argus-dev). OTLP endpoint inside the cluster:
`alloy.lgtm.svc:4317` (gRPC) / `:4318` (HTTP).

Tear down with `make dev-down`.

## Incident logging (do this from day one)

Module B (Backtest) needs labeled history. Every time you induce a fault (Chaos Mesh, kubectl,
manual) or observe a real incident on the dev cluster, append an entry to
[`incidents.yaml`](https://github.com/tamen25/Argus/blob/main/incidents.yaml) at repo root —
schema is documented in the file header.

## Try it without a cluster

`make demo` (Docker only; no Node or Go needed) starts the engine, Postgres and
`grafana/otel-lgtm` with the Argus plugin installed and pointed at the engine.
The engine sends its own telemetry to the demo's Collector, which mirrors
everything it receives back to the engine, so it scores itself from the start.

| | |
|---|---|
| Grafana, with Argus under Apps | <http://localhost:23000> (anonymous admin) |
| Engine API | <http://localhost:28080/api/report> |
| Send your own OTLP | `localhost:24317` (gRPC), `localhost:24318` (HTTP) |

Every port is bound to localhost, and the engine's API is served without a
token: it is a local demo. `make demo-down` removes it, data included.

## Everything else

```bash
make test              # unit tests
make test-integration  # testcontainers-based (needs docker)
make lint              # golangci-lint + eslint
make build             # engine binary + plugin dist
make demo              # docker compose demo stack
```
