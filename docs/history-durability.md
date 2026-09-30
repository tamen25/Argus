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

## When the mount detaches (after a Docker Desktop restart)

**Symptom:** after Docker Desktop restarts, `mimir-minio` sits in
`ContainerCreating` and Mimir components crashloop behind it. The MinIO pod's
events say:

```
MountVolume.SetUp failed for volume "history-sentinel" :
hostPath type check failed: /data/argus-history/.argus-history-sentinel is not a file
```

**Fix:** `make dev-heal` (from WSL).

**What happened.** The history reaches MinIO through a kind `extraMount` whose
source lives in the Ubuntu-24.04 distro. If Docker Desktop starts before that
distro is up, the source cannot resolve, and Docker quietly puts an *empty*
directory in its place. Seen twice (2026-07-25, 2026-09-30); it recurs.

**Why MinIO is held back on purpose.** Without a guard, MinIO meets the empty
stand-in and either:

- crashloops with `file access denied … Run: sudo chown -R … && sudo chmod
  u+rxw <path>` — advice that, if followed on the stand-in, makes MinIO start a
  **fresh, empty history** and orphans everything accumulated since Phase 0; or
- if the stand-in were ever writable, forks the history outright, silently.

So the history directory holds a sentinel file, `.argus-history-sentinel`, and
MinIO mounts it with `hostPath` `type: File` (`deploy/kind/values/mimir.yaml`).
Kubelet will not start the pod unless that exact file exists, so on a detached
mount MinIO never runs: no fork, and no misleading advice. The sentinel lives
*inside* the history, so it travels with it through backups and restores.
`bootstrap.sh` creates it if missing; it only ever runs in WSL, so it can only
write into the real directory.

**What `make dev-heal` does.** It checks whether the node sees the sentinel. If
not, it restarts the kind node container (WSL is necessarily up, since the
script runs there), waits for the API server to authorize requests, then waits
for MinIO and the ingester. It never writes to the history. If the sentinel is
missing on the *host* too, that is not a detached mount, and heal stops and says
so rather than guessing.

Verified 2026-09-30 by overlaying an empty tmpfs on the node's mount point (a
faithful detach: the node sees nothing, the host keeps everything): MinIO was
held with the event above, `make dev-heal` detected it, re-attached, and the
stack recovered with all 13 history blocks (2026-07-11 onward) intact.

After recovery, Mimir's label/metadata APIs may error for a while (`bucket
index is too old`) while the compactor catches up; instant queries work
meanwhile.

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
3. `make dev-up` — this also recreates the history sentinel, which backups taken
   before 2026-09-30 do not contain; without it MinIO would (correctly) refuse
   to start
4. Verify: query a metric from before the restore point in Grafana (Mimir
   datasource) with a time range covering the old window — old series must
   resolve. Also `kubectl -n lgtm logs sts/mimir-store-gateway | grep -i "loaded blocks"`.

## Layout on disk

`/var/lib/argus/history/` is MinIO's volume (`/export` in the pod):
`mimir-tsdb/` (TSDB blocks per tenant — `anonymous/` in dev) and `mimir-ruler/`
(ruler state). Don't hand-edit; use `make backup-history`.
