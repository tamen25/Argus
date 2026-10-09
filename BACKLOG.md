# BACKLOG

Open work: fixes, gaps, and Phase 4 scope. One line, then why it matters.
IDs are stable — reference them in commits and PRs (`Closes B-07`). When an item
ships, move it to **Done** with the PR number; never renumber.

Scope cuts and deviations go in `DECISIONS.md`, not here. This file is the
queue; that one is the ledger.

Last full sweep: 2026-09-30 — toolchain (golangci-lint, govulncheck, race
tests, plugin typecheck/lint/test/audit), coverage, a `code-review` pass over
PR #61, and the live kind cluster.

---

## Plan

Sequenced by what unblocks what. One PR per step; each merges green before the
next starts, so nothing sits unmerged.

1. ~~**Per-scenario reset/cleanup (B-10).**~~ Done. Unblocks `--repeats >1` on scenario 1
   (frontend must restart to drop the fault's cumulative series) and every
   mutation scenario. Includes the reset for scenario 1 and the coverage gap it
   touches (B-19).
2. ~~**Scenarios 2–5 fixed and live-validated (B-09, B-11, B-12, B-13).**~~ Done. Rebuild
   the stale `feat/bench-scenarios-2-5` branch on current main; gate latency
   faults on latency, add the egress-blackhole scenario, replace the OOMKill
   mechanism. Each one checked on kind: selector matches, fault bites, gate
   fires, baseline clears, calibration gives 0.00 / 1.00.
3. ~~**Floor of 8 (B-30).**~~ Done. Three mutation scenarios on top of step 1: broken
   trace propagation, missing `service.name`, deploy regression.
4. ~~**Release plumbing (B-20, B-37).**~~ Done. goreleaser + plugin zip, with the action
   major-version bumps. Required for the v1.0 exit gate.
5. ~~**Plugin hygiene (B-21–B-24).**~~ Done, except what is blocked upstream
   (B-24, and the rest of B-21).
6. **v1.0 artifacts (B-31–B-33).** Built and verified end to end on kind with the
   stub: the conditions (#81), the report and the Bench page (#74, #82), and the
   matrix runner (#85). What remains is the real run: choose the hosted models
   (B-46), confirm the runner's cost projection (master plan §12.4), run
   `docs/bench/flagship.md`, then tag v1.0.

Needs the user: B-46 (flagship models and spend cap), B-35 (catalog submission status).

## P2 — Plugin

- [ ] **B-21** — npm audit reports 11 (5 high), down from 13, with **zero shipped
  exposure**: each sits in or behind a package Grafana supplies at runtime
  (`@grafana/data|ui|runtime` and `react-router*` are webpack externals), and
  none of `moment`, `dompurify`, `react-use`, `js-cookie` or `react-router`
  appears in `dist/`. Clearing them needs `@grafana/*` 13.2 and React Router 7,
  which moves the SDK two minors past the pinned 13.0 line — a compatibility
  decision, not hygiene. **Don't** run
  `npm audit fix --force`; it bumps the Grafana SDK outside its declared range.
- [ ] **B-24** — `@stylistic/eslint-plugin-ts` is deprecated, but it is a peer
  dependency of `@grafana/eslint-config` 9, which is what the current
  create-plugin scaffold (7.11.0) still pins. `@grafana/eslint-config` 10 moves
  to `@stylistic/eslint-plugin` and drops the `./flat.js` export the scaffolded
  `.config/eslint.config.mjs` imports. **Blocked upstream**: migrate when
  create-plugin adopts eslint-config 10, by running its `update`.

---

## Phase 4 scope still open

Not defects. Tracked here so nothing is lost between sessions.

- [ ] **B-32** — The LLM judge (the fallback normalizer for shell agents) needs a
  model other than the agent's; the guard refuses the same model id.
- [ ] **B-46** — **Needs a decision.** Which hosted models make up the flagship
  agents (master plan §2: at least one frontier model, one budget model, and
  HolmesGPT), and the spend cap. With local inference removed (2026-10-01), every
  real run is a paid API call. Measured: one run is roughly 50k–110k tokens, so
  the floor matrix (48 runs) is about 2.5M–5M tokens per agent.
- [ ] **B-33** — Flagship artifacts. Done: the report generator (#74), the
  telemetry conditions (#81), the plugin Bench page (#82) and the matrix runner
  with its cost projection (#85). Still open: (c) the user's confirmation of
  that projection (§12.4 —
  confirm with the user before the first full run). The harness gaps that
  blocked it are fixed (B-39 to B-43); what remains is choosing the models
  (B-46).
- [ ] **B-34** — `deploy/terraform/` is a README placeholder. **Cut from v1.0**
  (DECISIONS.md, 2026-10-09): the flagship runs on kind; EKS is post-v1.0.
- [ ] **B-35** — Plugin catalog submission. §9 says to submit at Phase 4 *start*
  (2026-07-20); status unknown.

---

## Done

> **ID correction (2026-09-30).** #61's commit message says "Closes … B-24 B-25
> B-26"; it closed **B-25, B-26 and B-27** (judge timeout, EntityKey, shotgun
> decoys). B-24 (eslint deprecation) was not touched and is still open. The
> merged message cannot be edited, so the correction lives here.

- [x] **B-33c, runner** (#85) — `scripts/bench-matrix.sh` runs scenarios ×
  agents × conditions, switching each condition with `apply.sh`, resuming where
  it stopped, restoring baseline at the end, and writing the comparison. Without
  `--yes` it only prints the plan and the projected cost (§12.4); 144 runs
  project to about 9–14 hours and 7–15M tokens. Run book:
  `docs/bench/flagship.md`.
- [x] **B-33b** (#82) — the plugin's **Bench** page and `argus serve
  --bench-reports <dir>` (`/api/bench`, `?compare=` for the comparison): the
  leaderboard per telemetry condition and the degraded-vs-remediated comparison,
  with the reports' caveats always shown. Verified in the dev cluster's Grafana
  (12.3.1) against real run reports.
- [x] **B-33a** (#81) — `scenarios/conditions/apply.sh baseline|degraded|remediated`
  switches the dev cluster's telemetry with an Alloy pipeline stage and waits
  until the condition shows in the telemetry. Degraded: broken propagation and
  logs without trace context (our construction, detected by Argus's rules).
  Remediated: Argus's `missing-resource-attributes` patch, held to the template
  by a test. All three verified live.
- [x] **B-47** (#80) — 7 of the 12 remediation templates the rules named did not
  exist (13 of 18 rules), so `argus remediate` and the plugin's remediation
  panel failed for them — including ARG-RES-002, which fires on 17 of 18
  services here. All 12 now exist; a test fails if a rule names a missing
  template, and every Collector form passes `otelcol-contrib validate`.
- [x] **B-45** (#79) — `engine/argus` (a 53 MB build committed in #31) untracked and
  git-ignored. It remains in history: purging it needs a force push, which the
  `main` ruleset forbids.
- [x] **Local inference removed** (#78) — `deploy/ollama/`, the Ollama probe and
  context guard, `--local-only` and `--min-context`. Reports record the model id
  and endpoint; the judge guard compares model ids.
- [x] **B-43** (#77) — the token budget gets the same notice and final turn as
  the tool-call cap, triggered when the tokens left would not cover another turn
  like the last one; a diagnosis on the final turn is accepted even if that turn
  crosses the cap (the true count is reported). The report names the cap that
  ended a run. Unit- and mutation-tested; not yet confirmed on a real run.
- [x] **B-42** (#76) — discovery tools (`list_metrics`, `list_metric_labels`,
  `list_log_labels`, `list_trace_tags`) and `get_k8s_topology` (workload
  identities via `kubectl get`, plus the service graph; bench fault objects
  hidden). Query-tool descriptions point at them and carry neutral syntax
  examples. On a real run 15 of 17 calls succeeded, against 6 of 20 before.
- [x] **B-41** (#75) — every agent is offered a closed fault-category list
  (`scenarios/categories.yaml`): an enum on `submit_diagnosis`, and prose in the
  brief for agents that see no tool schema. `bench run` refuses to run without
  it, or with a scenario whose category is not on it. Verified on a real run:
  the model answered from the list.
- [x] **B-33, report generator** (#74) — `argus bench report` builds a leaderboard
  (agents × scenarios, one board per condition) or, with
  `--compare baseline,treatment`, the degraded-vs-remediated comparison from
  run reports. `bench run --condition` labels a run. Paired scenarios only, no
  zero-filling, pooled runs, and every like-for-like problem disclosed.
- [x] **B-31** (#73) — a real model has produced a scored diagnosis:
  `qwen3.6-bench` on `deploy-regression-cart`, 265 s, 20 tool calls, score 0.00
  (named `flagd`, not `cart`). Written up in `docs/bench/first-real-run.md`
  with the five harness findings it produced.
- [x] **B-39** (#73) — the agent is told its budget, and gets one turn to submit
  once the last tool call is used. The first real run was cut off at 20 calls
  having never been told a limit existed.
- [x] **B-40** (#73) — run reports carry a tool log (tool, arguments, error, result
  size; never the telemetry itself) and the category the agent gave.
- [x] **B-17** (#72) — `make dev-up` deploys the argus engine: `bootstrap.sh` builds
  the image from the checkout, loads it into kind and applies
  `deploy/kind/argus-engine.yaml` (`ARGUS_SKIP_ENGINE=1` opts out).
  `make dev-engine` redeploys after a code change. The soak harness uses the same
  script instead of its own copy of those steps.
- [x] **B-22, B-23** and the fixable part of **B-21** (#71) — `@grafana/*` 13.0.2 →
  13.0.10; `react-router-dom` moved to `devDependencies` (only a test imports
  it); in-range audit fixes applied; `uuid` overridden to the patched 11.1.1 —
  the one flagged package that does reach the bundle (through
  `@grafana/scenes`, `v4` only). `App.test.tsx` now mounts the app under
  `/a/<plugin id>/*` as Grafana does, so the Overview page really renders and the
  assertions can fail (mutation-tested by breaking the route); the React Router
  warnings are gone. create-plugin scaffold 7.8.1 → 7.11.0.
- [x] **B-20** (#70) — `release.yml` + `.goreleaser.yaml`: a `vX.Y.Z` tag builds
  the CLI for six platforms and the plugin zip (signed when
  `GRAFANA_ACCESS_POLICY_TOKEN` is set), with checksums, into a **draft**
  release. The toolchain comes from `engine/go.mod`, so B-07's concern is
  settled. CI runs `goreleaser check`; `docs/releasing.md` documents the steps.
  Validated locally: snapshot build of all six archives, `argus version`
  stamped, spec pin embedded, plugin zip in the checksums, mage build of all
  seven plugin backends.
- [x] **B-37** (#70) — actions at current majors (checkout v7, setup-go v7,
  setup-node v7, golangci-lint-action v9). The release notes were checked for
  renamed inputs; none of the ones used here changed.
- [x] **B-38** (#70) — stale docs: the README status said Phase 1 with "v0.1 tag
  pending", the quickstart said Phase 0 and Go ≥ 1.23 (the engine needs 1.25), and
  the bench docs were missing from the mkdocs nav.
- [x] **B-30** (#69) — scenario library at the floor of 8: missing `service.name`,
  broken trace propagation and a deploy regression, each an env-var mutation of
  a real workload through `faults/lib/env-fault.sh`, restored namespace-wide by
  `faults/restore-all-mutations.sh`. All three calibrate to 0.00/0.00/1.00 and
  pass the full lifecycle on kind. A test holds the floor.
- [x] **B-36** — PR #61 merged; `main` has moved on through #68.
- [x] **B-09, B-11, B-12, B-13** (#68) — scenarios 2–5 rebuilt and live-validated:
  latency faults gated on p95 (they produce no 5xx), the egress-blackhole
  scenario written, a real OOMKill via checkout's own memory limit. All five
  scenarios pass the full lifecycle on kind at 1.00 and calibrate to
  0.00/0.00/1.00. Also: the probe treats NaN as unknown (it passed the gate), and
  reset sweeps every argus-managed fault (a July leak sat on checkout for two
  months).
- [x] **B-10** (#67) — scenario-owned `reset`/`cleanup` hooks and
  `--inject=auto` (the new default); legacy modes refuse scenarios with hooks.
  Scenario 1 restarts frontend on reset and cleanup; its steadyState query now
  forgets dead series, so the baseline clears in ~2 min instead of ~5 (121 s vs
  302 s, measured).
- [x] **B-19** (#67) — `internal/bench` coverage 63% → 89%.
- [x] **B-28** (#67) — 31 remote branches deleted, each confirmed MERGED via its
  PR (restorable from the PR page). Kept: `release/v0.2-prep`, `release/v0.3`
  (merged, tags hold the release points — the maintainer's call), and
  `feat/bench-scenarios-2-5` (source for plan step 2).
- [x] **B-29** (#64) — stale bucket index after a compactor crashloop is
  documented in `docs/history-durability.md`.
- [x] **B-16** (#64) — history-mount guard: a sentinel file inside the history,
  mounted by MinIO as hostPath `type: File`, so kubelet refuses to start MinIO on
  a detached (empty) mount; `make dev-heal` re-attaches. Verified against a
  simulated detach; all history blocks intact.
- [x] **B-01..B-06** (#61) — the six scoring-integrity bugs from review: the judge
  extracts (never supplies) evidence; repeats inject only into a verified-clean
  baseline, with settle timing out of the shared probe (mutation-tested);
  malformed citations are scored 0, not dropped from the mean; judge/agent
  compared by weights digest (live-verified); `--local-only` opt-in with the
  num_ctx guard keyed to loopback endpoints (both removed 2026-10-01 with local
  inference).
- [x] **B-14** (#61) — "checkout emits only 10 series" corrected (cold-cluster
  reading); the retarget stands because checkout's rpc labels are bounded.
- [x] **B-15, B-27** (#61) — calibration tests load the shipped scenario; the stub
  requires `--stub-obvious`/`--stub-category`; `--stub-shotgun` names decoys.
- [x] **B-25, B-26** (#61) — `--judge-timeout`; one `EntityKey`; dead code removed;
  uncited rate and malformed citations rendered; the bench report gained its
  first golden-file test.
- [x] **B-07** (#63) — 34 reachable vulnerabilities → 0: engine 18 (stdlib ×14 via
  toolchain go1.26.8, grpc ×3 → v1.83.2, x/text ×1) and the plugin's Go backend
  16 (grafana-plugin-sdk-go v0.285 → v0.296.5). Lowest fixing versions, so the
  engine's minimum Go stays 1.25.
- [x] **B-08** (#63) — CI toolchain pinned (`go-version-file`, golangci-lint
  v2.14.0); govulncheck runs on both Go modules.
- [x] **B-18** (#63) — ≥70% coverage gate on rules, cost, backtest and
  bench/scoring; also fails if a gated package drops out of the report.
