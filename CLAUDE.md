# CLAUDE.md — Argus

<!-- Repo root file. Claude Code reads this automatically every session.
     Full specification: docs/argus-master-build-plan.md — this file is the
     operating contract; the master plan is the source of truth for WHAT to build.
     When they conflict, the master plan wins. -->

Repository: https://github.com/tamen25/Argus · License: Apache-2.0

## What this project is

Argus is an open-source observability quality platform: **CI for reliability**. One Go engine, four capabilities against a self-hosted Grafana LGTM stack (Loki, Tempo, Mimir + Alloy/OTel Collector):

- **A1 Score** — implements the Instrumentation Score spec (0–100 per service) via CEL rules over sampled OTLP + backend polling; findings carry confidence (sampled vs poller-verified)
- **A2 Spend** — cost attribution/showback for self-hosted LGTM (ingest, active series, S3 storage classes); prices A1 findings (`estimated_monthly_cost`)
- **B Backtest** — replays historical Mimir data against Prometheus/Mimir alert rules and SLO burn-rate policies; reports would-have-fired, TTD, false positives, pages/week; `backtest diff` gates CI; always reports fidelity caveats
- **C Prove** — fault-injection bench harness measuring whether AI agents (remote APIs / OpenAI-compatible endpoints / HolmesGPT via shell adapter) can diagnose incidents from this telemetry; ITBench-compatible; flagship result = degraded-vs-remediated telemetry → agent accuracy delta

UI: Grafana App Plugin (React + Scenes + Go plugin backend) + `argus` CLI (cobra). Storage: Postgres only. **No GPU, no CUDA, no local model hosting, no eBPF anywhere in this project.**

**Read `docs/argus-master-build-plan.md` in full before your first task — especially §12 (known risks) before each phase. When neither doc specifies something, follow existing codebase patterns; if none exist, ask — never invent architecture silently.**

## Current phase

> Update this section manually as phases complete. Do not work ahead of the current phase.

- [x] **Phase 0 (done 2026-07-12)** — Foundation: WSL2 env, kind bootstrap (LGTM + otel-demo + chaos-mesh), monorepo scaffold, CI, Helm skeleton, **start telemetry-history accumulation** (long Mimir retention + `incidents.yaml` from day one)
- [ ] **Phase 1 (ACTIVE)** — Score engine (15 rules target / 8 floor) + remediation templates + CLI + plugin Overview/Scores → v0.1 public
- [ ] Phase 2 — Cost engine + LLM explanations + plugin Spend → v0.2 public
- [ ] Phase 3 — **Fidelity spike first (timeboxed 1 week)** → backtest engine + synth-history generator + plugin Backtest → v0.3 public
- [ ] Phase 4 — Submit plugin to catalog at phase START → bench orchestrator + scenarios (12 target / 8 floor) + flagship benchmark → v1.0 public

**Phase gates are hard.** Each phase ends with a tagged release, blog-ready artifact, and green CI. Feature counts are cut targets; gates are not. Record every cut in `DECISIONS.md`. If asked to jump ahead, refuse and point here.

## Repo layout

```
engine/           Go module — cmd/argus/ (CLI+server), internal/{ingest,model,rules,cost,remediate,backtest,bench,mcp,store}, pkg/api/
plugin/           Grafana app plugin (@grafana/create-plugin scaffold, Scenes)
rules/            Built-in rule YAML (spec rules vs argus-extension rules, separated)
scenarios/        Bench scenarios + faults/ manifests
deploy/           helm/argus/, kind/ (bootstrap), terraform/ (EKS, apply/destroy per session only)
docs/             mkdocs-material + argus-master-build-plan.md + backtest-fidelity.md (Phase 3 spike output)
DECISIONS.md      every scope cut / deviation, one line + rationale
```

## Binding architecture rules

1. **Hexagonal**: every external system (Mimir, Loki, Tempo, S3, LLM, K8s, Chaos Mesh) sits behind an interface in the consuming package; concrete clients only in adapter packages. Unit tests use fakes; testcontainers only in `*_integration_test.go`.
2. **Deterministic core, LLM at the edge**: `rules/`, `cost/`, `backtest/`, `bench/scoring` never import the LLM client (depguard-enforced). LLM only explains findings, drafts remediation text, and (clearly flagged) normalizes shell-agent bench output. LLM output never affects scores and is never auto-applied.
3. **Bounded memory in ingest**: HLL/count-min sketches and aggregates only. Never persist raw telemetry; findings keep ≤5 truncated evidence samples. Benchmark test asserts O(1) steady-state allocations.
4. **Rules are data**: YAML + CEL, versioned schema, strict loader. Common-case rule = zero Go changes.
5. **Read-only product**: no write credentials to user systems. Remediations are generated files (Alloy River / Collector YAML) a human applies.
6. **Reproducible benchmarks**: every bench run records scenario hash, agent config, model version, env digest, seed, token/tool-call budgets. Run-matrix economics are binding (master plan §3.2): full matrix on kind only, EKS for headline subset only, per-run budget caps enforced.
7. **Honest reporting**: confidence on findings, fidelity caveats on backtests, budgets and normalization method on bench reports. Argus never overstates certainty.
8. LLM redaction (`llm.redact`) defaults on — attribute values stripped before any LLM call.

## Tech stack (do not substitute)

Go ≥1.23 · cel-go · prometheus/prometheus (promql + rules, as libraries) · OTel collector pdata for OTLP · cobra · pgx + golang-migrate · axiomhq/hyperloglog · grafana-plugin-sdk-go · @grafana/create-plugin + @grafana/scenes + React 18 + strict TS · goreleaser · golangci-lint + eslint · testcontainers-go · Playwright (plugin smoke) · GitHub Actions · Conventional Commits · SemVer.

## Commands

```bash
make dev-up          # kind cluster: LGTM + otel-demo + chaos-mesh + argus (Phase 0 deliverable)
make dev-down
make test            # unit tests, all modules
make test-integration
make lint            # golangci-lint + eslint + depguard checks
make build           # engine binary + plugin dist
make demo            # docker-compose: engine + postgres + grafana(+plugin) + mini-LGTM
```

Create these targets in Phase 0; keep them working forever. CI runs lint + test + build on every PR; no direct pushes to `main` after Phase 0.

## Quality bar

- Coverage ≥70% on `rules/`, `cost/`, `backtest/`. Golden-file tests for every rule (sample OTLP in → findings out) and every report format.
- Every new finding type, CLI command, or plugin page ships with its docs page in the same PR.
- Dogfood gate (from end of Phase 1): the engine's own OTel telemetry must pass `argus score --fail-below-score 85` in CI.

## Working with the user

- Senior SRE: expert in LGTM/OTel/K8s/AWS/Terraform, strong TypeScript, **newer to deep Go** — explain non-obvious Go idioms in PR/commit descriptions; write idiomatic Go regardless.
- Dev environment: WSL2 Ubuntu 24.04 on Windows. EKS via `deploy/terraform` for flagship runs only — always remind the user to destroy after use. Before the first full Phase 4 run, compute projected cost (wall-clock, API tokens, EKS hours) and confirm with the user.
- Prefers complete file contents over partial diffs when showing config files.
- Positioning discipline in all public-facing text (README, docs, release notes): Argus is an independent open-source *implementation* of the Instrumentation Score spec and a collaborator to OllyGarden/Weaver — never framed as a competitor. ITBench compatibility is cited, not claimed as novel.

## Out of scope for this repo

- Anything GPU/CUDA/local-model-hosting related; anything eBPF (user's separate upstream-contribution track — never add eBPF code here)
- Multi-tenant SaaS control plane, SSO/RBAC, PDF exec reports (future BSL modules — keep interfaces clean, build nothing)
- Any write-path automation against user infrastructure
