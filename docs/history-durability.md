# Telemetry history durability

Module B (Backtest) depends on months of accumulated Mimir history (master plan
§9, §12 risk 3). The dev cluster is disposable; the history is not.

## Architecture

```
Mimir (blocks, 365d retention)
  └─ S3 API ──► MinIO (in-cluster, chart dependency)
                  └─ PVC argus-history (static, storageClass argus-history)
                       └─ PV hostPath /data/argus-history   (kind node)
                            └─ extraMount /var/lib/argus/history  (WSL host ext4)
```

Mimir stays object-storage-native (the same S3 code path the Phase 2 cost
engine inspects). Durability comes from the layer underneath: MinIO's data
directory is a kind `extraMount` onto the WSL host filesystem, outside the
cluster's lifecycle.

- `make dev-down` / `kind delete cluster` — history **survives**
- `make dev-up` on a fresh cluster — MinIO mounts the same directory; Mimir's
  store-gateway/compactor pick the old blocks up automatically
- **Lost on recreation:** data not yet flushed to blocks (ingester/Kafka WAL —
  roughly the last 2h). Acceptable for backtest purposes.
- **Not covered:** unregistering the Ubuntu-24.04 WSL distro deletes
  /var/lib/argus. Take a backup first.

## Backup

```bash
make backup-history                     # -> ~/argus-backups/argus-history-<ts>.tgz
BACKUP_DIR=/mnt/g/backups make backup-history   # somewhere else (e.g. Windows drive)
```

Consistent enough live (blocks are immutable once written; a block mid-upload
just gets re-uploaded). For a guaranteed-clean snapshot, `make dev-down` first.

## Recovery

1. `make dev-down` (or start from no cluster)
2. Restore: `rm -rf /var/lib/argus/history && tar xzf argus-history-<ts>.tgz -C /var/lib/argus`
3. `make dev-up`
4. Verify: query a metric from before the restore point in Grafana (Mimir
   datasource) with a time range covering the old window — old series must
   resolve. Also `kubectl -n lgtm logs sts/mimir-store-gateway | grep -i "loaded blocks"`.

## Layout on disk

`/var/lib/argus/history/` is MinIO's volume (`/export` in the pod):
`mimir-tsdb/` (TSDB blocks per tenant — `anonymous/` in dev) and `mimir-ruler/`
(ruler state). Don't hand-edit; use `make backup-history`.
