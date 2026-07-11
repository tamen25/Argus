# DECISIONS

Every scope cut / deviation from the master plan. One line + rationale. Newest first.

- 2026-07-12 — Helm chart versions in `deploy/kind/bootstrap.sh` pinned via variables at top of script; initial pins taken from repo state at scaffold time — re-pin after first successful `make dev-up`. Rationale: reproducibility without blocking Phase 0 on chart churn.
- 2026-07-12 — `release.yml` (goreleaser + plugin zip) deferred to Phase 1: first tagged release is the Phase 1 exit gate; Phase 0 ships `ci.yml` only.
- 2026-07-12 — Master plan moved from repo root to `docs/argus-master-build-plan.md` to match the layout both docs specify.
