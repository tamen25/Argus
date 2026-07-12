# DECISIONS

Every scope cut / deviation from the master plan. One line + rationale. Newest first.

- 2026-07-12 — Durable history via in-cluster MinIO on a hostPath-backed static PV (kind extraMount → /var/lib/argus/history on WSL ext4), not an external MinIO container: Mimir keeps its object-storage-native path, everything stays declarative in the repo, and no out-of-band container lifecycle to manage. Unflushed WAL (~2h) is accepted as loss on recreation.
- 2026-07-12 — flagd-ui sidecar disabled on the kind cluster: OOMKills within seconds at 250Mi/512Mi/1Gi (runaway allocation). Flag editing not needed for telemetry; Phase 4 fault injection will edit the flagd ConfigMap directly.
- 2026-07-12 — grafana/alloy Helm chart (1.10.1) prints a deprecation warning; kept for Phase 0 since it works. Revisit successor chart (k8s-monitoring / alloy-operator) before Phase 1 wiring of the argus fan-out.

- 2026-07-12 — Helm chart versions in `deploy/kind/bootstrap.sh` pinned via variables at top of script; initial pins taken from repo state at scaffold time — re-pin after first successful `make dev-up`. Rationale: reproducibility without blocking Phase 0 on chart churn.
- 2026-07-12 — `release.yml` (goreleaser + plugin zip) deferred to Phase 1: first tagged release is the Phase 1 exit gate; Phase 0 ships `ci.yml` only.
- 2026-07-12 — Master plan moved from repo root to `docs/argus-master-build-plan.md` to match the layout both docs specify.
