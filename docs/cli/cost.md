# `argus cost`

Attribute and price your self-hosted LGTM spend — showback from backend
usage. Gathers active series (Mimir), log bytes (Loki), and object-storage
inventory (S3/MinIO), prices them against a `pricing.yaml`, models storage
lifecycle savings, and — with `--store-dsn` — reports week-over-week trends.

```bash
argus cost \
  --pricing ./pricing/aws.yaml \
  --mimir-url http://mimir-gateway.lgtm.svc \
  --loki-url  http://loki-gateway.lgtm.svc \
  --s3-bucket argus-blocks --s3-endpoint http://minio.lgtm.svc:9000 --s3-path-style \
  --window 1h \
  --store-dsn "postgres://argus:…@postgres/argus" \
  --output md
```

Configure **at least one** source — Argus refuses to print an empty report
that would misleadingly read as `$0`.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--pricing` | *(required)* | path to `pricing.yaml` |
| `--window` | `1h` | measurement window; ingest bytes are extrapolated to a month |
| `--mimir-url` / `--mimir-tenant` | | active-series attribution |
| `--loki-url` / `--loki-tenant` | | log-bytes attribution |
| `--s3-bucket` / `--s3-prefix` | | object-storage inventory |
| `--s3-endpoint` / `--s3-region` / `--s3-path-style` | | MinIO / non-AWS endpoints |
| `--service-label` | `service_name` | label used to attribute by service |
| `--store-dsn` | | persist this snapshot and trend against the last |
| `--output` | `md` | `md` or `json` |
| `--out` | *(stdout)* | write to a file instead |
| `--fail-over-monthly` | `0` | exit non-zero when total monthly cost exceeds this (CI budget gate) |

## CI budget gate

`--fail-over-monthly` exits non-zero when the modeled monthly total exceeds a
budget — the cost analogue of `argus score --fail-below-score`:

```bash
argus cost --pricing pricing/aws.yaml --mimir-url … --fail-over-monthly 5000
```

## Honesty

Costs are **modeled from your rates, not billed**, and every report says so.
Attribution from a sampled mirror carries the same sampling caveats as scores;
poller-derived volumes (which see everything) are preferred. See
[Cost & showback](../cost.md) for the model.
