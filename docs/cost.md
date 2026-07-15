# Cost & showback (Spend)

Argus prices your self-hosted LGTM stack: what each service, team, and signal
*costs*, and — the signature move — the monthly cost of every quality finding.
*"Score 61, and here's the invoice for why."*

> **Phase 2, in progress.** This page documents the pricing model and the
> deterministic cost core (shipped). Pollers, per-finding pricing, the
> `argus cost` CLI, and the plugin Spend page land across the remaining
> Phase 2 slices.

## The pricing model is data

Self-hosted LGTM has no per-GB invoice — the cost is the compute and storage
you run it on. Argus turns your own modeled unit rates into attributed
dollars via a versioned `pricing.yaml`:

```yaml
schema: argus.pricing/v1
currency: USD

ingest:
  per_gb: 0.35                 # $/GB ingested (extrapolated to a monthly bill)
  per_gb_by_signal:            # optional per-signal overrides
    logs: 0.30
    traces: 0.40

active_series:
  per_million: 8.00            # $/million active series-month (Mimir's driver)

storage:
  per_gb_month_by_class:       # $/GB-month by object-storage class
    STANDARD: 0.023
    GLACIER_IR: 0.004
```

Two templates ship in [`pricing/`](https://github.com/tamen25/Argus/tree/main/pricing):

- **`aws.yaml`** — AWS S3 list prices for storage classes plus illustrative
  amortized compute rates. A starting point; calibrate the compute rates to
  your own EKS/EBS spend.
- **`generic.yaml`** — every rate `0` (cloud-agnostic skeleton to fill in).

The loader is **strict**: an unknown key is an error, because a mistyped rate
that silently prices at zero is worse than a failed load. The schema tag is
checked so a future format bump fails loudly instead of parsing to nonsense.

## Where the usage comes from

`Usage` is measured by backend sources, each behind an interface (the cost
core never imports a concrete client). Every source is optional — a stack with
only Mimir still produces an active-series report — and `Gather` composes
whatever is wired, returning any source error rather than presenting a partial
report as complete.

| Source | Backend | Query | Feeds |
|---|---|---|---|
| `SeriesSource` | Mimir | cardinality API, series per `service_name` | active-series cost (metrics) |
| `LogBytesSource` | Loki | `sum by (service_name) (bytes_over_time(…[window]))` | log ingest cost |
| `StorageSource` | S3 / MinIO | object inventory by storage class | storage cost *(slice 4)* |

Metric **ingest-byte** attribution is deliberately not inferred from the
sampled mirror; metrics cost is attributed through active series (Mimir's real
driver), which the cardinality API reports exactly. Trace-byte attribution
lands with the storage inventory.

## How usage becomes cost

The cost core takes that `Usage` value and prices it. Pricing is
**deterministic**: the same usage and rates always produce the same report,
byte-for-byte.

| Input | Unit | Extrapolation |
|---|---|---|
| Ingest bytes | flow over the measurement window | scaled to a month (`730h ÷ window`) |
| Active series | point-in-time gauge | priced directly, **not** scaled |
| Storage bytes | point-in-time gauge | priced directly (rate is already per-month) |

Ingest is a rate: a GB/hour flow becomes a monthly bill. Active series and
stored bytes are gauges — a snapshot count priced against a monthly rate — so
they are never multiplied by the window. Mixing those two up is the classic
showback error; Argus keeps them distinct and documents which is which in
every report.

## Honesty

Costs are **modeled, not billed** — they are exactly as accurate as the rates
you supply, and the report says so. Attribution from a sampled telemetry
mirror carries the same sampling caveats as scores; poller-derived volumes
(which see everything) are preferred for cost wherever available.
