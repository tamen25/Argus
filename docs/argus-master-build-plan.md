# ARGUS — Master Build Plan

> Repository: **https://github.com/tamen25/Argus** · License: Apache-2.0 · Codename confirmed available.

---

## 0. How to use this document (instructions to the build agent)

- This document is the single source of truth. Read it fully before writing any code.
- Work **phase by phase, in order**. Do not start a phase until the previous phase's **Exit Gate** is met. Exit gates are non-negotiable: they exist to prevent 85%-done syndrome.
- Within a phase, work milestone by milestone. Each milestone ends with passing tests and a green CI run.
- **Feature counts inside phases are cut targets, not gates.** If time pressure hits, cut features (15 rules → 8, 12 scenarios → 8) and record the cut in `DECISIONS.md`. Never cut quality gates, never cut the public exit artifact, never let a phase silently sprawl.
- When a decision is not specified here, prefer: (1) the pattern already established in the codebase, (2) the idiomatic choice in the Go / Grafana plugin ecosystems, (3) ask the user. Never invent a new architectural pattern silently.
- All configuration examples in this doc are normative. Match names, paths, and formats exactly.
- The user is a Senior SRE (Grafana LGTM, OTel, K8s, AWS, Terraform expert; strong TypeScript; newer to Go at depth). Explain non-obvious Go idioms in PR descriptions; don't dumb down the code.

---

## 1. Product thesis and positioning

**One-liner:** *CI for reliability. Argus scores your telemetry, prices it, backtests your alerts against history, and proves — with an AI-agent benchmark — whether your observability can actually diagnose an incident.*

**The problem.** Reliability teams test application code with CI, but deploy their observability configuration on faith. Nobody knows: whether their instrumentation follows conventions well enough to be diagnosable, what each label and log stream actually costs, what their alert rules' false-positive rate is until 3 AM, or whether a human or AI agent could find root cause with the telemetry they emit. Frontier AI SRE agents fail more than half of benchmark incidents, and the industry consensus is that telemetry quality — not model quality — is the bottleneck.

**The product.** One data plane, four verbs:

| Module | Verb | Question answered |
|---|---|---|
| **A1 — Score** | score | Is my instrumentation any good? (Instrumentation Score spec implementation) |
| **A2 — Spend** | price | What does each service/team/label cost me in my self-hosted LGTM stack? |
| **B — Backtest** | backtest | Would my alert rules and SLO burn-rate policies have caught my past incidents, and at what page load? |
| **C — Prove** | prove | Can an agent (LLM or human runbook) diagnose injected faults using my telemetry — and does fixing my score improve its accuracy? |

**Differentiation vs. prior art (be precise about this in all public copy):**
- **OTel Weaver**: dev/CI-time schema validation. Argus is production-stream, fleet-wide, continuous. Argus *uses* semconv registries; it does not replace Weaver.
- **Instrumentation Score spec / OllyGarden**: Argus is an independent open-source *implementation* of the open spec (their product is commercial). Argus extends beyond scoring into cost attribution, alert backtesting, remediation generation, and the agent benchmark. Tone in public: collaborator, not competitor. Contribute rules upstream to `instrumentation-score/spec`.
- **Sloth / Pyrra**: generate SLO rules. Argus *simulates* rules against history. Complementary.
- **ITBench / SREGym**: offline/academic benchmarks. Argus Module C runs on a *live LGTM environment* and is scenario-compatible with ITBench for citable comparability.
- **AI SRE vendors (Rootly, incident.io, etc.)**: they are Argus's benchmark *subjects*, not competitors.

**License:** Apache-2.0 for everything in the monorepo at launch. Reserve the following as future BSL/commercial modules (do NOT build in v1, but keep interfaces clean so they can be added later): multi-tenant SaaS control plane, SSO/RBAC beyond Grafana's, scheduled executive PDF reports, fleet-of-clusters aggregation.

**Explicitly out of scope for this project:** anything GPU-dependent. No local model hosting requirement, no CUDA, no eBPF. Agents and LLM features run against remote APIs or any user-supplied OpenAI-compatible endpoint.

---

## 2. Success criteria (what "done" means for the human)

1. Grafana App Plugin published to the Grafana plugin catalog (or at minimum signed + installable from a release URL).
2. `argus` CLI installable via `go install` / GitHub release binaries.
3. Flagship benchmark report published: agent RCA accuracy on degraded vs. remediated telemetry across the scenario suite and ≥3 agents (≥1 frontier API, ≥1 budget API model, HolmesGPT), with repeat-run variance stats.
4. Four public artifacts, one per phase (§9): tagged release + blog post each.
5. ≥2 rules contributed upstream to `instrumentation-score/spec` (the user separately pursues OBI contributions outside this repo, §10).

---

## 3. Architecture

### 3.1 System overview

```
                        ┌─────────────────────────────────────────────┐
                        │                Grafana                      │
                        │   ┌──────────────────────────────────┐      │
                        │   │  Argus App Plugin (React/Scenes) │      │
                        │   └──────────────┬───────────────────┘      │
                        └──────────────────┼──────────────────────────┘
                                           │ HTTP (plugin backend proxy)
                                           ▼
┌───────────────┐   OTLP (sampled)  ┌─────────────────────────────────┐
│ Alloy /       │──────────────────▶│        argus-engine (Go)        │
│ OTel Collector│                   │  ┌──────────┐  ┌─────────────┐  │
└───────────────┘                   │  │ Ingest   │  │ Rule Engine │  │
                                    │  │ (OTLP    │  │ (CEL rules, │  │
┌───────────────┐   HTTP APIs       │  │ receiver │  │ score calc) │  │
│ Mimir / Loki /│──────────────────▶│  │ + pollers│  ├─────────────┤  │
│ Tempo / S3    │                   │  └──────────┘  │ Cost Engine │  │
└───────────────┘                   │  ┌───────────┐ ├─────────────┤  │
                                    │  │Remediation│ │ Backtest    │  │
┌───────────────┐  OpenAI-compat /  │  │ Generator │ │ Engine      │  │
│ LLM endpoints │  Anthropic APIs   │  │ (+LLM)    │ ├─────────────┤  │
│ (remote APIs) │◀──────────────────│  └───────────┘ │ Bench       │  │
└───────────────┘                   │                │ Orchestrator│  │
                                    │  Postgres (state) + /metrics    │
                                    └─────────────────────────────────┘
                                           ▲
                              ┌────────────┴────────────┐
                              │ argus CLI (cobra)       │  ← CI/CD usage
                              └─────────────────────────┘
```

### 3.2 Data plane components (Go, single binary `argus-engine`, subsystems as internal packages)

**Ingest layer** — two vantage points, both read-only:
1. **OTLP receiver** (gRPC :4317 + HTTP :4318): receives a *sampled mirror* of production telemetry. Deployment pattern: users add a second exporter in Alloy pointing at Argus (document the `otelcol.exporter.otlp` block + a `probabilistic_sampler` in front). Argus never sits in the critical path. Bounded memory is a hard requirement: process-and-discard; only findings, sketches, and aggregates are stored — never raw telemetry bodies.
2. **Backend pollers**: Mimir (Prometheus HTTP API: `/api/v1/*`, cardinality endpoints, TSDB status), Loki (`/loki/api/v1/*`, series/label APIs, ingestion stats), Tempo (search/metadata APIs), S3 (bucket inventory + storage-class breakdown via AWS SDK; support MinIO for kind). All backend clients live behind interfaces (§5.1).

**Sampling-awareness (required):** because the OTLP view is sampled, low-volume services can statistically evade stream-based rules. Every finding carries a `confidence` field derived from observed sample counts; rules that can be evaluated from backend pollers (which see everything) prefer that path. Reports must distinguish "verified" (poller-backed) from "sampled" findings.

**Module A1 — Score engine.**
- Implements the Instrumentation Score spec: per-service score 0–100, weighted boolean rules, impact levels (Critical/Important/Normal/Low). Follow the spec's published formula exactly; vendor the spec's rules directory as a git submodule or generated Go, and track spec version in `.instrumentation-score-version`.
- Rules are **data, not code**: YAML rule definitions with **CEL** (`cel-go`) expressions evaluated against a normalized telemetry-item model (span, metric datapoint, log record, resource). Target ≥15 rules at Phase 1 (cutable to 8; see §6.1 catalog). Custom user rules load from a directory.
- Cardinality analysis uses **HyperLogLog sketches** (e.g. `axiomhq/hyperloglog`) per (metric, label) pair — never exact sets. Top-K offenders via count-min sketch + heap.
- Scores are persisted to Postgres (history) **and** exposed as Prometheus metrics (`argus_instrumentation_score{service=...}`) so users can dashboard/alert on their own score — self-referential and demo-gold.

**Module A2 — Cost engine.**
- Inputs: poller data (per-tenant/per-stream ingest bytes, active series, trace bytes), S3 storage-class inventory, and a **pricing config** (`pricing.yaml`: $/GB-ingested, $/GB-month by storage class, $/million-active-series, currency). Ship AWS-default and generic templates.
- Outputs: cost attribution by service, team (via configurable label, e.g. `team`), signal type, and label; S3 lifecycle modeling ("moving traces >14d to Glacier IR saves $X/mo"); trend deltas week-over-week; showback report (Markdown + JSON).
- Every A1 finding that has a cost dimension gets priced: findings carry an optional `estimated_monthly_cost` computed from the cost engine. This is the product's signature move — "score 61, and here's the invoice for why."

**Remediation generator.**
- For each finding type, a deterministic **patch template** produces an Alloy config snippet (River) or OTel Collector processor YAML: e.g., `attributes` processor to drop/hash a high-cardinality label, `probabilistic_sampler`/`tail_sampling` blocks, Loki `drop` stages, relabel rules injecting `service.name`.
- The **LLM layer** (OpenAI-compatible client; endpoint/model/key in config — any remote API or user-supplied compatible endpoint) does exactly two things: (1) writes the human-readable explanation of a finding and its patch, (2) drafts remediation for finding types with no template. **LLM output is never applied automatically and never affects scores.** Deterministic core, LLM at the edge. A `redact: true` config (default on) strips attribute *values* (keeps keys) from anything sent to the LLM.
- Output: a `remediations/` directory of patch files + a PR-ready description. CLI: `argus remediate --finding <id> --format alloy|otelcol`.

**Module B — Backtest engine.**
- Embed the Prometheus rule-evaluation machinery as a library (`github.com/prometheus/prometheus/promql` + `rules` packages). Load the user's real alert rule files (Prometheus/Mimir ruler format; also accept Sloth/Pyrra-generated files) and evaluate them over historical data via `query_range` against Mimir, stepping through time (default step: rule's evaluation interval; configurable for speed vs. fidelity).
- **MANDATORY FIRST TASK OF PHASE 3 — fidelity spike (1 week, timeboxed):** historical replay has known failure modes that must be characterized before building the full engine: (a) rules referencing *recording rules* cannot be replayed unless those recording rules were running historically — implement dependency detection + a "recording-rule synthesis" mode that evaluates the recording rule's expression inline, with documented accuracy caveats; (b) `for:` clauses and staleness semantics differ under range-query replay vs. live evaluation — quantify the divergence on known cases; (c) Mimir retention bounds the backtest window — detect and report the usable window instead of failing silently; (d) series renamed/relabeled over time break continuity — detect gaps and flag affected rules. The spike's output is a `docs/backtest-fidelity.md` design note; the engine's report footer always states fidelity caveats that applied.
- **Incident registry**: `incidents.yaml` — user-labeled past incidents (`start`, `end`, `title`, `services`, optional `expected_alerts`). This is the ground truth.
- Report per rule and per rule-set: would-have-fired timeline, time-to-detection per incident, missed incidents, false positives (fires outside any incident window ± grace period), estimated pages/week, flappiness. For SLOs: burn-rate simulation across multi-window policies (support the standard 5m/1h + 30m/6h fast/slow pairs and custom policies).
- Killer CLI feature: `argus backtest diff --rules-a current/ --rules-b proposed/` → side-by-side report. This is the CI gate use case: fail the pipeline if the proposed rules regress detection or exceed a pages/week budget.
- **Demo-data dependency (see §9 Phase 0):** backtesting needs months of history + labeled incidents. Mitigations start day one: long retention on the demo environment's Mimir, incident logging as faults/chaos occur, and a **synthetic history generator** (`argus devtools synth-history`) that fabricates realistic multi-week metric series with embedded incident signatures for demos and tests.

**Module C — Bench orchestrator ("prove").**
- **Scenario spec** (YAML, one file per scenario):
  ```yaml
  apiVersion: argus/v1alpha1
  kind: BenchScenario
  metadata: {name: cardinality-explosion-checkout}
  spec:
    environment: {app: otel-demo}           # target workload
    inject:                                  # ordered fault steps
      - type: chaosmesh                      # or: kubectl, script
        manifest: faults/cardinality-explosion.yaml
        duration: 10m
    groundTruth:
      rootCauseEntities:                     # ITBench-style entity list
        - {kind: Deployment, namespace: otel-demo, name: checkout}
      category: cardinality-explosion
    scoring: {entityMatch: jaccard, partialCredit: true}
  ```
- **Agent adapters** (interface `BenchAgent`): (1) OpenAI-compatible chat agent with tool use (covers any API or user-supplied endpoint), (2) Anthropic API agent, (3) shell adapter wrapping existing open-source agents (HolmesGPT, K8sGPT) as subjects. Agents must return a structured diagnosis JSON (schema in `bench/schema/diagnosis.json`); for shell agents whose output format we don't control, a normalization step maps their output to the schema — normalization is deterministic where possible, with an LLM-judge fallback that is clearly flagged in results.
- **Tool surface for agents**: an **MCP server** (`argus mcp`) exposing read-only tools: `query_prometheus`, `query_loki`, `search_traces`, `get_k8s_topology`, `list_alerts`. Same surface for every agent = fair comparison; also independently useful/marketable.
- **Orchestrator**: reset env → inject fault → wait for steady failure state → hand agent the alert + tool access → collect diagnosis → score vs groundTruth → repeat N times → variance stats.
- **Run-matrix economics (binding):** the naive full matrix (12 scenarios × 3 agents × 3 repeats × 2 telemetry conditions = 216 runs at 15–30 min each) is weeks of wall-clock and real money. Controls: (a) full matrix runs on **kind only**, with scenarios parallelized across namespaces where faults don't interfere; (b) **EKS is reserved for a headline subset** (~2 scenarios × all agents × both conditions) for the "live cloud infrastructure" claim; (c) per-run API budget caps: max tool calls and max tokens per agent per run, enforced by the orchestrator and reported; (d) scenario `duration` defaults short (10 min) with steady-state detection to end early.
- **ITBench importer**: converter from ITBench scenario definitions to Argus spec, so published baselines are comparable.
- **The flagship experiment** (this is the whole point): run the scenario suite twice — once against the environment with deliberately degraded telemetry (missing `service.name`, broken trace propagation, no exemplars), once after applying Argus remediations. Report accuracy delta per agent. Headline: *"Fixing telemetry quality moved agent RCA accuracy from X% to Y% — quality, not model choice, is the bottleneck."*

### 3.3 Control plane

**Grafana App Plugin** (`argus-app`, React + TypeScript, **Scenes** framework, plugin backend in Go using the Grafana plugin SDK — the backend proxies to argus-engine so the browser never needs direct engine access; auth via plugin settings: engine URL + token).

Pages (left nav of the app):
1. **Overview** — fleet score (big number + trend sparkline), total monthly telemetry spend, top 5 findings by cost, backtest health summary, last bench result. This is the screenshot for the README.
2. **Scores** — service table: score, trend, findings count, cost; drill into per-service finding list; each finding shows rule, evidence sample, confidence, impact, estimated cost, and a "View remediation" panel with the generated patch (code block, copy button) + LLM explanation.
3. **Spend** — cost treemap (service → signal → label), team showback table, S3 lifecycle savings recommendations, trend chart.
4. **Backtest** — rule-set picker, incident timeline with fire/miss markers, per-rule stats table, diff view (A vs B rule sets), pages/week gauge, fidelity-caveat banner.
5. **Bench** — scenario catalog, run launcher (dev convenience; flagship runs happen via CLI), leaderboard: agent × scenario matrix with accuracy + variance, degraded-vs-remediated comparison chart.
6. **Settings** — engine connection, pricing config editor, LLM endpoint config, incident registry editor.

Design language: use `@grafana/ui` components exclusively; do not fight Grafana theming. Follow Grafana plugin review guidelines from day one (signing, `plugin.json` metadata, provisioning docs) so catalog submission is not a rewrite. Note: catalog review has multi-week lag — submit at Phase 4 start, not end.

**CLI** (`argus`, cobra): `score`, `cost`, `backtest [diff]`, `bench run|import|report`, `remediate`, `mcp`, `devtools synth-history`, `report export --format md --out ./vault/` (Obsidian-compatible Markdown export with frontmatter). Every command supports `--output json` and CI-friendly exit codes (`--fail-below-score 70`, `--fail-on-regression`).

### 3.4 Storage
- **Postgres** (docker-compose/Helm dependency): findings, score history, cost snapshots, backtest runs, bench runs. Migrations via `golang-migrate`, schema in `engine/internal/store/migrations/`. (No SQLite dual-backend in v1 — one storage path, less maintenance; `make demo` ships Postgres in compose.)
- Prometheus metrics endpoint on the engine for self-observability + score export. The engine instruments itself with OTel (dogfooding; Argus must score well on its own telemetry — CI check enforces exactly that from end of Phase 1).

---

## 4. Repository and tooling

Monorepo: `github.com/tamen25/Argus`

```
Argus/
├── engine/                  # Go module
│   ├── cmd/argus/           # CLI + engine entrypoints (serve, score, backtest, bench, mcp, devtools...)
│   ├── internal/
│   │   ├── ingest/          # otlp receiver, pollers (mimir/, loki/, tempo/, s3/)
│   │   ├── model/           # normalized telemetry item model
│   │   ├── rules/           # CEL engine, rule loader, score calc
│   │   ├── cost/            # cost engine, pricing
│   │   ├── remediate/       # patch templates (templates/*.river.tmpl), llm/
│   │   ├── backtest/        # promql replay, incident registry, reports, synth-history
│   │   ├── bench/           # orchestrator, adapters/, scenarios loader, scoring, itbench importer, schema/
│   │   ├── mcp/             # MCP server tools
│   │   └── store/           # postgres
│   └── pkg/api/             # HTTP API (chi or stdlib mux), OpenAPI spec committed
├── plugin/                  # Grafana app plugin (create-plugin scaffold)
├── rules/                   # built-in rule YAML (spec rules vs argus-extension rules, separated)
├── scenarios/               # bench scenarios + faults/ manifests
├── deploy/
│   ├── helm/argus/          # chart: engine + postgres, values for kind & EKS
│   ├── kind/                # kind config + bootstrap script (LGTM via helm, otel-demo, chaos-mesh)
│   └── terraform/           # EKS demo env (thin layer referencing user's CloudOps patterns; apply/destroy per session)
├── docs/                    # mkdocs-material: quickstart, architecture, rule authoring, bench guide, backtest-fidelity.md
├── DECISIONS.md             # every scope cut / deviation, one line + rationale
└── .github/workflows/       # ci.yml (lint/test/build), release.yml (goreleaser + plugin zip), dogfood-score check
```

**Stack (definitive):** Go ≥1.23; `cel-go`; `prometheus/prometheus` (promql, rules); OTel Go SDK + `collector/pdata` for OTLP; `spf13/cobra`; `jackc/pgx`; `golang-migrate`; `axiomhq/hyperloglog`; `grafana-plugin-sdk-go`; plugin: `@grafana/create-plugin`, `@grafana/scenes`, React 18, TS strict; releases: `goreleaser`; lint: `golangci-lint` + `eslint`; tests: Go stdlib + `testcontainers-go` (Postgres, Mimir/Loki/Tempo single-binary modes), Playwright smoke test for plugin; CI: GitHub Actions; commits: Conventional Commits; versioning: SemVer, `v0.x` until Phase 4.

**Dev environment:** user's machine is Windows → **WSL2 Ubuntu 24.04** for all engine/plugin dev; **kind inside WSL2** with the LGTM Helm charts + OpenTelemetry Demo + Chaos Mesh. No GPU/CUDA setup anywhere in this project. EKS is only for flagship bench runs and demo recordings — Terraform apply/destroy per session; never leave it running.

---

## 5. Implementation practices and patterns (binding)

### 5.1 Architecture rules
1. **Hexagonal/ports-and-adapters**: every external system (Mimir, Loki, Tempo, S3, LLM, K8s, Chaos Mesh) is an interface in the consuming package with a client implementation in `ingest/` or `bench/adapters/`. No package imports a concrete client except its own adapter package. All unit tests against fakes; testcontainers only in `*_integration_test.go`.
2. **Deterministic core, LLM at the edge**: nothing in `rules/`, `cost/`, `backtest/`, or `bench/scoring` may call the LLM (depguard-enforced). Exception: the clearly-flagged LLM-judge fallback in bench output *normalization* (not scoring math).
3. **Bounded memory everywhere in ingest**: sketches and aggregates only; benchmark test asserts O(1) steady-state allocations per item.
4. **Rules as data**: adding a rule must require zero Go changes for the common case (YAML + CEL). Rule schema is versioned; loader rejects unknown fields.
5. **Everything reproducible**: bench runs record scenario hash, agent config, model version, env manifest digest, seed, and token/tool-call budgets. A bench report that can't be reproduced is a bug.
6. **Read-only by design**: the engine holds no write credentials to any user system. Remediations are files the human applies.
7. **Honest reporting**: findings carry confidence; backtest reports carry fidelity caveats; bench reports carry budget caps and normalization method. Argus never overstates certainty — that is the brand.

### 5.2 Quality bar
- CI green = merge; no direct pushes to `main` after Phase 0.
- Coverage gate 70% on `rules/`, `cost/`, `backtest/`; golden-file tests for every rule (sample OTLP in → expected findings out) and every report format.
- Every finding type, CLI command, and plugin page gets a docs page in the same PR it ships in.
- Dogfood gate (from end of Phase 1): the engine's own telemetry must pass `argus score --fail-below-score 85` in CI.

### 5.3 Security/privacy
- No raw telemetry persisted; findings store bounded evidence samples (max 5, values truncated).
- LLM redaction mode defaults **on**.
- Helm chart ships NetworkPolicies; engine API requires bearer token; plugin backend is the only documented caller.

---

## 6. Feature list (v1, complete)

### 6.1 Module A1 — Score (Phase 1) — target 15 rules, cut floor 8 (priority order)
1. Missing/UNKNOWN `service.name`
2. High-cardinality metric attribute (HLL estimate > threshold)
3. Broken context propagation (orphaned client spans / parent-not-found rate)
4. Logs without trace correlation (`trace_id` present in <X% of request-path logs)
5. Unbounded span attribute (raw URL/user ID in span name or attribute)
6. Missing `service.version` / `deployment.environment.name`
7. Log level abuse (DEBUG/INFO > threshold % of volume in prod env)
8. No exemplars on histogram metrics for traced services
9. Span name explosion (distinct span names per service > threshold)
10. Broken trace: missing root span
11. Metric with no unit / non-UCUM unit
12. Histogram bucket misconfiguration
13. Deprecated semconv attributes in use (vendored registry version)
14. Duplicate telemetry (same metric via multiple pipelines; hybrid eBPF+SDK double spans)
15. Resource attribute inconsistency (same service, conflicting resource attrs)
Plus: per-service + fleet score, score history, Prometheus export, custom rule loading, confidence field, JSON/Markdown report, CI gate flag.

### 6.2 Module A2 — Spend (Phase 2)
Ingest attribution (service/team/signal/label) from Mimir+Loki+Tempo APIs; active-series pricing; S3 storage-class inventory + lifecycle savings modeling; pricing.yaml with AWS template; per-finding `estimated_monthly_cost`; showback report (MD/JSON); week-over-week trends; plugin Spend page.

### 6.3 Remediation (templates in Phase 1, LLM polish in Phase 2)
Patch templates for rules 1, 2, 4, 5, 7 minimum; Alloy (River) + Collector YAML output; LLM explanations + drafts (OpenAI-compatible remote endpoint, redaction on); `argus remediate`; plugin remediation panel.

### 6.4 Module B — Backtest (Phase 3)
Fidelity spike + design note (mandatory first); rule-file loading (Prometheus/Mimir ruler, Sloth/Pyrra output); historical replay vs Mimir with recording-rule synthesis mode; incident registry; per-rule + per-set reports (TTD, misses, false positives, pages/week, flappiness); multi-window burn-rate simulation; synthetic history generator; `backtest diff` + CI regression gate; plugin Backtest page with incident timeline + caveat banner.

### 6.5 Module C — Prove (Phase 4) — target 12 scenarios, cut floor 8 (priority order)
Cardinality explosion; broken trace propagation; missing service.name; deploy regression (bad image); OOMKill cascade; network policy block; DNS failure; Redis latency injection; disk pressure → Loki ingestion drop; alert storm (noisy rule); certificate expiry; S3 backend throttling. Each with ground truth + degraded/remediated environment variants. Agent adapters (OpenAI-compatible, Anthropic, shell/HolmesGPT); diagnosis JSON schema + normalization; MCP server; orchestrator with repeats, variance, and budget caps; run-matrix controls per §3.2; ITBench importer; leaderboard page; flagship report generator (`bench report --compare degraded,remediated`).

---

## 7. LLM integration spec
- Single client (`remediate/llm`): OpenAI-compatible chat completions; config: `llm.endpoint`, `llm.model`, `llm.api_key_env`, `llm.redact (default true)`, `llm.max_tokens`. Anthropic native client as a second implementation of the same interface.
- No local-model hosting is part of this project. Any OpenAI-compatible endpoint the user supplies (remote or self-hosted) works by construction; document that in one paragraph and move on.
- Prompt templates live in `remediate/llm/prompts/*.tmpl`, versioned, with golden tests on template rendering (not on model output).
- Bench agents use the same client types but separate config (`bench.agents[]`, each with `max_tool_calls` and `max_tokens_per_run`), so product-LLM and benchmark-subject-LLM are never conflated.

---

## 8. UI implementation guide (plugin)
- Scaffold with `npx @grafana/create-plugin@latest` (app plugin, with backend). Target current Grafana LTS; test on the kind cluster's Grafana.
- Scenes for all data-bound pages; engine data via plugin backend resource endpoints (`/resources/scores`, `/costs`, `/backtests`, `/bench`) proxying the engine API with the stored token.
- Copy discipline: numbers first, adjectives never. Every panel answers one question stated in its title.
- Screenshot/demo path (README + catalog listing): Overview → drill into worst service → open remediation patch → Spend treemap → Backtest diff → Bench leaderboard. Optimize these six screens; everything else can be plain tables.
- Ship provisioning docs + `docker-compose up` demo (engine + Postgres + Grafana with plugin preinstalled + tiny LGTM) so anyone can see it in 5 minutes without a cluster.

---

## 9. Phase plan and exit gates (6 months nominal — see §12 timeline risk)

**Phase 0 — Foundation (weeks 1–2).** WSL2 env; kind bootstrap script (LGTM + otel-demo + chaos-mesh, one command); monorepo scaffold; CI; Helm skeleton; **history accumulation starts now**: set long Mimir retention on the demo/CloudOps environment, create `incidents.yaml`, and log every induced fault from day one so Phase 3 has real data.
*Exit gate:* `make dev-up` gives a working kind cluster with telemetry flowing; CI green on a hello-world engine + scaffolded plugin; history accumulation running.

**Phase 1 — Score + Remediate core (weeks 3–8).** Ingest (OTLP + Mimir/Loki pollers minimum); rule engine + rules (target 15 / floor 8); score calc + history + Prometheus export; 5 remediation templates; CLI `score`/`remediate`; plugin Overview + Scores pages; docs quickstart.
*Exit gate (public):* **v0.1 tagged**, blog post #1 ("An open-source Instrumentation Score engine for the LGTM stack"), posted to CNCF #instrumentation-score + r/sre + LinkedIn; first rule PR opened upstream to the spec.

**Phase 2 — Spend (weeks 9–13).** Cost engine; S3 inventory + lifecycle modeling; per-finding pricing; showback report; LLM explanations; plugin Spend page.
*Exit gate (public):* **v0.2**, blog post #2 ("Showback for self-hosted LGTM: what your labels cost"), demo GIF.

**Phase 3 — Backtest (weeks 14–19).** Fidelity spike (week 1, timeboxed) → PromQL replay; incident registry; synthetic history generator; reports; burn-rate simulation; `backtest diff` + CI gate; plugin Backtest page.
*Exit gate (public):* **v0.3**, blog post #3 ("Backtest your alerts like a trading strategy") — expect this one to travel; submit a talk abstract (KubeCon/Grafana meetup/SREcon) on the full platform.

**Phase 4 — Prove + flagship (weeks 20–26).** Submit plugin to Grafana catalog at phase START (review lag). Bench orchestrator; scenarios (target 12 / floor 8); 3 agent adapters; MCP server; ITBench importer; kind full-matrix runs + EKS headline subset per §3.2 economics; leaderboard page; flagship report.
*Exit gate (public):* **v1.0**, flagship post ("We benchmarked AI SRE agents on live infrastructure. Telemetry quality was the bottleneck.") with full methodology + repro instructions.

**Buffer:** 2 weeks unallocated. If any phase slips >2 weeks, cut features within the phase (documented in `DECISIONS.md`), never quality gates, never the public exit artifact.

---

## 10. Parallel track (NOT for the build agent in this repo — human calendar item)
~4 hrs/week: OBI (`opentelemetry-ebpf-instrumentation`) upstream contributions and Instrumentation Score spec rule contributions. Keep strictly out of the Argus repo.

## 11. Resume bullets this project is engineered to produce
- Built and open-sourced an observability quality platform (Go, CEL, Grafana Scenes) implementing the Instrumentation Score specification; published Grafana App Plugin.
- Designed an alert-backtesting engine replaying N days of production metrics against Prometheus/Mimir rules, quantifying false-positive rates and time-to-detection before deploy.
- Published a live-environment benchmark linking telemetry quality to AI SRE agent root-cause accuracy (X scenarios, Y agents), with reproducible methodology.
- Contributor, Instrumentation Score specification (rules) and OpenTelemetry eBPF Instrumentation (parallel track).

---

## 12. Known risks and standing mitigations (read before every phase)

1. **Timeline is calibrated near-full-time; the user builds nights/weekends.** Realistic multiplier 1.5–2×. The system absorbs this by design: gates require *shipped* artifacts, feature counts are cut targets. Slipping is acceptable; sprawling is not.
2. **Backtest fidelity** is the biggest technical risk → mandatory Phase 3 spike (§3.2). Never present replay results without caveats.
3. **No historical data** for Module B demos → history accumulation from Phase 0 + synthetic generator (§3.2, §9).
4. **Phase 4 run-matrix cost** (time + API tokens + EKS) → binding economics controls (§3.2). Compute the projected cost before the first full run and confirm with the user.
5. **Sampled-stream blind spots** → confidence fields + poller-verified findings (§3.2).
6. **Instrumentation Score spec is young and may change** → vendored, version-pinned, spec version in every report.
7. **Grafana catalog review lag** → submit at Phase 4 start.
8. **Community positioning**: any public text framing Argus against OllyGarden/Weaver as competitors is a bug. Collaborator tone, always.
