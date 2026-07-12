# DECISIONS

Every scope cut / deviation from the master plan. One line + rationale. Newest first.

- 2026-07-12 — SPA-003 threshold: upstream criteria is TODO; Argus ships max_span_names=200 as initial default, flagged for tuning before v0.1.
- 2026-07-12 — SPA-002/ARG-SPA-002 tolerate 10%/20% bad-trace ratios by default: head sampling makes orphan/rootless ratios upper bounds, so zero-tolerance would false-positive on any sampled pipeline. Documented in rule pages.
- 2026-07-12 — Master-plan rules 3+10 implemented via a bounded TraceTracker (LRU traces, capped spans/trace, completed-window judgement); trace_health has no poller verification — span topology is invisible to backend APIs, findings stay sampled.
- 2026-07-12 — Aggregate windows are two-generation tumbling (report max of current+previous), not sliding: HLL cannot subtract. Boundary behavior documented in docs/rules/authoring.md; bursts remain visible up to 2 windows.
- 2026-07-12 — Aggregate store admission is LRU with hard cap, default lowered 10000→4096 pairs/generation (memory envelope: ≈ cap × 16KiB dense × 2 gens worst case). Evictions counted and exported (argus_aggregate_pair_evictions_total), configurable via --max-tracked-pairs.
- 2026-07-12 — Built-in rules embedded via go:generate copy (engine/internal/rules/builtin) because go:embed cannot cross the module boundary to /rules; /rules stays source of truth, CI fails on drift, --rules dir overrides/extends by ID. Closes the v0.1 rule-loading item.
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
