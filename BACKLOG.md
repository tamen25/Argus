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
3. **Floor of 8 (B-30).** Three mutation scenarios on top of step 1: broken
   trace propagation, missing `service.name`, deploy regression.
4. **Release plumbing (B-20, B-37).** goreleaser + plugin zip, with the action
   major-version bumps. Required for the v1.0 exit gate.
5. **Plugin hygiene (B-21–B-24).** One small PR.
6. **v1.0 artifacts (B-31–B-33).** A real scored run, the second (judge) model,
   the flagship `--compare` report and leaderboard. The run-matrix cost
   projection is confirmed with the user before the first full run (master plan
   §12.4).

Anytime: B-17 (deploy argus from `dev-up`). Needs the user: B-35 (catalog submission status).

## P1 — Infrastructure

- [ ] **B-17** — `make dev-up` does not deploy argus, though the Makefile comment
  said it did (comment corrected alongside this backlog).
  `deploy/kind/argus-engine.yaml` exists, but `bootstrap.sh` never applies it.

## P2 — Quality gates

- [ ] **B-20** — No `release.yml`; v0.1–v0.3 were cut by hand. The v1.0 exit gate
  needs goreleaser + plugin zip. With B-07, a released binary's stdlib depends
  on whoever built it.

## P2 — Plugin

- [ ] **B-21** — npm audit reports 12 (7 high) with **zero shipped exposure**. Prod
  `src/` imports none of them, `dist/module.js` contains none, and
  `@grafana/*`/`react`/`react-router` are externals Grafana supplies. The real
  fix: `react-router-dom` is used only in a test, so move it to
  `devDependencies`. **Don't** run `npm audit fix --force`; it bumps the Grafana
  SDK outside its declared range.
- [ ] **B-22** — Bump `@grafana/*` from 13.0.2 to the current 13.x patch.
- [ ] **B-23** — `App.test.tsx` logs React Router v7 future-flag warnings, and its
  assertion `expect(container).toBeInTheDocument()` cannot fail.
- [ ] **B-24** — `@stylistic/eslint-plugin-ts` is deprecated; migrate to
  `@stylistic/eslint-plugin`.

## P3 — Cleanup

- [ ] **B-37** — GitHub Actions pinned to old majors (`checkout@v4`,
  `setup-go@v5`, `setup-node@v4`, `golangci-lint-action@v7`; current are v5+,
  v7, v5+, v9). They run on the deprecated Node 20 runtime, which is why every
  CI run warns. Bump deliberately, one PR, watching for input renames.

---

## Phase 4 scope still open

Not defects. Tracked here so nothing is lost between sessions.

- [ ] **B-30** — Scenario library is at 5/8 of the floor, all five live-validated.
  Remaining §6.5 priorities (broken trace propagation, missing `service.name`,
  deploy regression) are script steps with restore hooks, now possible (B-10).
- [ ] **B-31** — No real model has produced a scored diagnosis yet. The harness is
  validated end to end; the environment blocked it.
- [ ] **B-32** — The LLM judge needs a second local model; B-05's guard rightly
  blocks self-judging.
- [ ] **B-33** — Not started: flagship report (`bench report --compare
  degraded,remediated`), leaderboard page, run-matrix cost projection (§12.4 —
  confirm with the user before the first full run).
- [ ] **B-34** — `deploy/terraform/` is a README placeholder; the EKS headline
  environment is unbuilt.
- [ ] **B-35** — Plugin catalog submission. §9 says to submit at Phase 4 *start*
  (2026-07-20); status unknown.
- [ ] **B-36** — PR #61 has been open and unreviewed since 2026-07-25. Nothing has
  merged to `main` since #60.

---

## Done

> **ID correction (2026-09-30).** #61's commit message says "Closes … B-24 B-25
> B-26"; it closed **B-25, B-26 and B-27** (judge timeout, EntityKey, shotgun
> decoys). B-24 (eslint deprecation) was not touched and is still open. The
> merged message cannot be edited, so the correction lives here.

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
  num_ctx guard keyed to loopback endpoints.
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
