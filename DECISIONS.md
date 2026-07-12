# DECISIONS

Every scope cut / deviation from the master plan. One line + rationale. Newest first.

- 2026-07-12 — Master-plan rule 1 split in two: RES-005 implements the spec criteria verbatim (non-empty service.name); the UNKNOWN/SDK-default check is extension rule ARG-RES-001, because the spec forbids non-spec rules in the spec score and `unknown_service:x` is technically non-empty.
- 2026-07-12 — Spec vendored as verbatim file copies under rules/spec/upstream (pinned SHA in .instrumentation-score-version) instead of a git submodule: no submodule friction in CI/create-plugin tooling, same auditability.
- 2026-07-12 — Report rendering lives in engine/internal/report (not listed in the §4 layout): keeps rules/ deterministic-core-only and reusable by cost/backtest reports later. Flagged in the Phase 1 design review.
- 2026-07-12 — CLI loads rules from --rules dir (default ./rules) rather than embedding them in the binary: rules/ stays the single source of truth; revisit embedding for `go install` UX before v0.1 tag.
- 2026-07-12 — Durable history via in-cluster MinIO on a hostPath-backed static PV (kind extraMount → /var/lib/argus/history on WSL ext4), not an external MinIO container: Mimir keeps its object-storage-native path, everything stays declarative in the repo, and no out-of-band container lifecycle to manage. Unflushed WAL (~2h) is accepted as loss on recreation.
- 2026-07-12 — flagd-ui sidecar disabled on the kind cluster: OOMKills within seconds at 250Mi/512Mi/1Gi (runaway allocation). Flag editing not needed for telemetry; Phase 4 fault injection will edit the flagd ConfigMap directly.
- 2026-07-12 — grafana/alloy Helm chart (1.10.1) prints a deprecation warning; kept for Phase 0 since it works. Revisit successor chart (k8s-monitoring / alloy-operator) before Phase 1 wiring of the argus fan-out.

- 2026-07-12 — Helm chart versions in `deploy/kind/bootstrap.sh` pinned via variables at top of script; initial pins taken from repo state at scaffold time — re-pin after first successful `make dev-up`. Rationale: reproducibility without blocking Phase 0 on chart churn.
- 2026-07-12 — `release.yml` (goreleaser + plugin zip) deferred to Phase 1: first tagged release is the Phase 1 exit gate; Phase 0 ships `ci.yml` only.
- 2026-07-12 — Master plan moved from repo root to `docs/argus-master-build-plan.md` to match the layout both docs specify.
