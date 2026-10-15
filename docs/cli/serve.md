# `argus serve`

Run the Argus engine as a service: the HTTP API the Grafana plugin reads,
`/metrics` for Prometheus, and, with `--otlp-grpc`, an OTLP receiver that
scores telemetry continuously.

```bash
ARGUS_API_TOKEN=… argus serve \
  --addr :8080 \
  --otlp-grpc :4317 \
  --cost-pricing ./pricing/aws.yaml --cost-mimir-url http://mimir-gateway.lgtm.svc \
  --backtest-rules ./alerts.yaml --backtest-mimir-url http://mimir-gateway.lgtm.svc \
  --bench-reports ./runs/flagship/reports/pass-1
```

The Helm chart (`deploy/helm/argus`) and the kind manifest
(`deploy/kind/argus-engine.yaml`) run exactly this.

## Endpoints

Every endpoint is a `GET`; anything else is answered `405`.

| Path | Needs | What it serves |
|---|---|---|
| `/healthz` | | `ok`: liveness and readiness |
| `/metrics` | | Prometheus: engine metrics and, with ingest, the score gauges |
| `/api/report` | `--otlp-grpc` | the score report, as `argus score` writes it; findings are priced when `--cost-pricing` is set |
| `/api/aggregates` | `--otlp-grpc` | raw aggregate rows (`argus rules calibrate` input) |
| `/api/servicegraph` | `--otlp-grpc` | caller→callee edges joined with scores |
| `/api/remediation?rule=&service=` | `--otlp-grpc` | the patch for a finding in the current snapshot; `404` for any other |
| `/api/cost` | `--cost-pricing` | the cost showback ([cost](cost.md)) |
| `/api/backtest` | `--backtest-rules`, `--backtest-mimir-url` | a rolling-window backtest ([backtest](backtest.md)) |
| `/api/bench?compare=a,b` | `--bench-reports` | the bench leaderboard and condition comparison ([bench](bench.md)) |

An endpoint whose feature is not configured answers `404`, and the plugin
shows "not configured" rather than an empty page.

## Score gauges

With ingest, `/metrics` reports the latest snapshot, refreshed every
`--score-interval` (30s by default):

| Metric | Labels | Value |
|---|---|---|
| `argus_instrumentation_score` | `service` | the service's spec score, 0–100 |
| `argus_instrumentation_score_fleet` | | the mean over the services |
| `argus_findings` | `service`, `impact` | open findings |

A scrape always reports one whole snapshot. Until a service has been
observed there are no score series at all: an empty fleet's score would be a
vacuous 100.

## Authentication

When `ARGUS_API_TOKEN` is set, every `/api/` request needs
`Authorization: Bearer <token>`. Anything else gets `401`. `/healthz` and
`/metrics` stay open for probes and the Prometheus scrape: they carry no
findings.

Without the variable the API would be open, so `serve` only starts without it
when `--addr` is a loopback address (`127.0.0.1:8080`, `localhost:8080`), or
with `--insecure-no-auth` to serve an open API on purpose. `:8080` listens on
every interface, so it needs one or the other. The engine logs a warning
whenever the API is open.

The Helm chart generates the token into a Secret and sets the variable. Give
the same token to the Grafana plugin, on its configuration page
([plugin](../plugin.md)). The plugin backend is the API's only intended
caller; the chart's NetworkPolicy also limits which namespaces can reach it.

## OTLP receiver: who may send

The OTLP receiver (`--otlp-grpc`) has no authentication and no TLS. Whoever
can reach it can report telemetry under any service name, and so affect the
scores. The trust boundary is the network: in Kubernetes, the chart's
NetworkPolicy admits OTLP only from `networkPolicy.otlpAllowedNamespaces`, and
the API only from `networkPolicy.allowedNamespaces` and the release namespace.
A NetworkPolicy only holds on a CNI that enforces it; kind's default kindnet
does not. The receiver holds at most one 4 MiB message per export and 16
exports in flight per connection.

## Health check

`argus healthcheck` GETs `http://127.0.0.1:8080/healthz` (`--url`) and exits 0
on HTTP 200. The image is distroless, with no shell or curl, so a container
health check runs the binary itself:
`healthcheck: {test: ["CMD", "argus", "healthcheck"]}`.

## Timeouts

The server waits at most 5 s for request headers and 30 s for a request, and
closes idle connections after 2 minutes. There is no write timeout: the first
`/api/backtest` request of a cache period replays a week of history before it
answers, which can take minutes.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` | HTTP listen address |
| `--insecure-no-auth` | off | serve `/api` without `ARGUS_API_TOKEN` on a non-loopback `--addr` |
| `--otlp-grpc` | *(off)* | OTLP gRPC listen address; enables live scoring and the score endpoints |
| `--rules` | | extra rule directory, overriding built-ins with the same ID |
| `--score-interval` | `30s` | how often scores are recomputed and exported |
| `--cardinality-window` | `1h` | tumbling window for cardinality aggregates |
| `--max-tracked-pairs` | `4096` | cardinality sketch cap per window generation (LRU eviction beyond) |
| `--spec-version-file` | `.instrumentation-score-version` | pinned spec version, echoed in reports |
| `--cost-*` | | `/api/cost`; see [cost](cost.md) |
| `--backtest-*` | | `/api/backtest`; see [backtest](backtest.md) |
| `--bench-reports` | | `/api/bench`: a directory of `bench run --format json` reports |

The engine exports its own telemetry when `OTEL_EXPORTER_OTLP_ENDPOINT` is
set; CI points it at a second engine that scores the first (the dogfood gate).
The scheme chooses the transport: `https://` uses TLS with the system's root
certificates; `http://` or a bare `host:port` is plaintext, the usual setting
inside a cluster. `OTEL_EXPORTER_OTLP_INSECURE=true` forces plaintext.

Outbound HTTP clients (Mimir, Loki, Tempo, model endpoints) do not follow
redirects: some send credentials in custom headers (`X-Scope-OrgID`,
`x-api-key`), which a redirect would hand to the host it names. Configure the
URL a redirect points to instead.
