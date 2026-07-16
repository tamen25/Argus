# Cost & showback (Spend)

Argus prices your self-hosted LGTM stack: what each service, team, and signal
*costs*, and — the signature move — the monthly cost of every quality finding.
*"Score 61, and here's the invoice for why."*

> **Shipped in v0.2** — pricing model, pollers, per-finding pricing, the
> `argus cost` CLI, and the plugin Spend page. The dollar figures are only as
> real as your `pricing.yaml`: the shipped templates are illustrative (S3
> storage classes use AWS list prices; ingest and active-series rates are
> estimates to calibrate), and every report says so.

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
| `StorageSource` | S3 / MinIO | object inventory by storage class | storage cost |

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

## Pricing findings — the signature move

Every quality finding that has a cost dimension is priced: `argus score`
attaches an `estimated_monthly_cost` so the report reads *"score 61, and here's
the invoice for why."*

Which findings are priced is **data, not code** — a rule declares a `cost:`
block naming the pricing driver and the finding field that carries the
cost-bearing quantity:

```yaml
# rules/spec/met-001.yaml
cost:
  driver: active_series      # priced against active_series.per_million
  quantity_field: cardinality
```

For `MET-001` (bounded metric-attribute cardinality), each distinct value of a
high-cardinality label drives roughly one active series for the metric, so the
observed cardinality × the active-series rate is that label's monthly cost.
The pricer reads the quantity from the finding (poller-verified `Details`
first, else the worst truncated evidence sample, so cost is never understated)
and leaves any finding it can't price **unset** — never a fabricated `$0`.

This is an honest **lower bound**: label combinations multiply series further,
and the estimate is flagged as an estimate. New drivers (log bytes, trace
bytes) extend pricing to more rule types without touching the pricer.

## Storage lifecycle savings

Argus inventories object storage by class (S3 `ListObjectsV2`, streamed so
memory stays bounded over millions of objects; MinIO works via the same
S3-compatible API on the kind cluster) and models cold-tiering savings:

> Moving 1,000 GB from `STANDARD` to `GLACIER_IR` costs \$4.00/mo instead of
> \$23.00/mo — **\$19.00/mo saved.**

`DefaultLifecycleRules()` supplies the common transition candidates; each is
kept only when the source class actually holds bytes and the target is both
**priced and cheaper** on *your* pricing — an unpriced class is unknown, not
free, and is never recommended. Whether the data is cold enough to tolerate a
slower-retrieval class is your call; Argus only prices the move.

## Week-over-week trends

Each priced report is persisted to Postgres (`cost_snapshots`, full report as
JSONB plus queryable total). `Trend(current, previous)` computes per-line and
total deltas so showback answers *"what moved, and by how much?"* — a line new
this week shows as a full increase, a vanished line as a full decrease.

Percent change is **0 against a zero baseline**: a brand-new cost line has no
prior to divide by, so it is never reported as infinite growth. The first ever
run trends against an empty baseline rather than failing.

## Live endpoint (plugin Spend page)

`argus serve --cost-pricing pricing.yaml --cost-mimir-url … --cost-loki-url …`
exposes **`/api/cost`**, the showback JSON the plugin's Spend page reads
(through its backend proxy). The result is cached for `--cost-cache-ttl`
(default 1m) so the page's polling never hammers the backends, and — with
`--cost-store-dsn` — each refresh persists a snapshot for week-over-week
trends. With no `--cost-pricing`, `/api/cost` returns 404 and the page shows
"not configured" rather than a misleading `$0`.

## Honesty

Costs are **modeled, not billed** — they are exactly as accurate as the rates
you supply, and every rendering (Markdown, JSON, Spend page) carries two
standing caveats: *modeled from your pricing.yaml, not billed* and *shipped
template rates are illustrative — calibrate them*. There is no self-hosted
invoice to be right against, so Argus never lets a modeled number pose as a
billed one. Attribution from a sampled telemetry
mirror carries the same sampling caveats as scores; poller-derived volumes
(which see everything) are preferred for cost wherever available. Lifecycle
recommendations price a transition; they never assess whether your retention
or retrieval requirements permit it.
