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

## P1 — Scenarios 2–5 (live-validated 2026-07-25; 3 of 4 broken as committed)

Branch `feat/bench-scenarios-2-5`, never PR'd. Selectors all match (1 pod each);
the gates and mechanisms are what failed.

- [ ] **B-09** — `redis-latency-cart`: frontend p95 goes from 21 ms to 10 s but with
  **zero 5xx**, so the 5xx-keyed gate never fires. Gate on latency instead.
- [ ] **B-10** — Reset and cleanup hooks are global CLI flags (`--reset-script`,
  `--cleanup-script`). Mutation faults and B-03 need restore steps owned by the
  scenario.
- [ ] **B-11** — `network-partition-product-catalog`: also no 5xx. Gate on latency.
- [ ] **B-12** — `dns-failure-frontend`: DNSChaos does not inject on this cluster
  (chaos-daemon panics rewriting resolv.conf; `AllInjected=False`). The
  egress-blackhole manifest replacing it is written and verified (5xx
  0.150/s), but its scenario YAML is missing.
- [ ] **B-13** — `oomkill-checkout`: StressChaos injects, but the OOM killer takes the
  stressor (largest process in checkout's 20 Mi cgroup), not checkout. No restart,
  and the gate never fires. Needs a different mechanism.

## P1 — Infrastructure

- [ ] **B-16** — The kind history bind mount **detaches on every Docker Desktop
  restart** that beats the Ubuntu distro up (seen 2026-07-25 and 2026-09-30).
  Docker substitutes an empty root-owned dir; MinIO crashloops and takes Mimir
  with it. Crashing is the *lucky* outcome: a writable empty dir would silently
  fork the history kept since Phase 0. Add a sentinel file, a preflight that
  refuses to start without it, and `make dev-heal`.
- [ ] **B-17** — `make dev-up` does not deploy argus, though the Makefile comment
  said it did (comment corrected alongside this backlog).
  `deploy/kind/argus-engine.yaml` exists, but `bootstrap.sh` never applies it.

## P2 — Quality gates

- [ ] **B-19** — `internal/bench` coverage fell from 83% to 69%:
  `SteadyState.validate`/`SettleDur` are untested.
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

## P3 — Cleanup

- [ ] **B-27** — The `shotgun` stub profile names generic services, so via the CLI
  it never hits a scenario's decoys.
- [ ] **B-28** — ~19 stale remote branches from July. They were squash-merged, so
  `git branch --merged` reports them all unmerged; check each PR's state before
  deleting.
- [ ] **B-29** — After a compactor crashloop (B-16), Mimir's bucket index goes
  stale (>1 h), and label/metadata APIs error until the compactor catches up.
  Instant queries still work. Add to the dev runbook.
- [ ] **B-37** — GitHub Actions pinned to old majors (`checkout@v4`,
  `setup-go@v5`, `setup-node@v4`, `golangci-lint-action@v7`; current are v5+,
  v7, v5+, v9). They run on the deprecated Node 20 runtime, which is why every
  CI run warns. Bump deliberately, one PR, watching for input renames.

---

## Phase 4 scope still open

Not defects. Tracked here so nothing is lost between sessions.

- [ ] **B-30** — Scenario library is at 5/8 of the floor, and four of those need
  B-09–B-13. Remaining §6.5 priorities (broken trace propagation, missing
  `service.name`, deploy regression) need B-10.
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

- [x] **B-01..B-06** (#61) — the six scoring-integrity bugs from review: the judge
  extracts (never supplies) evidence; repeats inject only into a verified-clean
  baseline, with settle timing out of the shared probe (mutation-tested);
  malformed citations are scored 0, not dropped from the mean; judge/agent
  compared by weights digest (live-verified); `--local-only` opt-in with the
  num_ctx guard keyed to loopback endpoints.
- [x] **B-14** (#61) — "checkout emits only 10 series" corrected (cold-cluster
  reading); the retarget stands because checkout's rpc labels are bounded.
- [x] **B-15, B-26** (#61) — calibration tests load the shipped scenario; the stub
  requires `--stub-obvious`/`--stub-category`; `--stub-shotgun` names decoys.
- [x] **B-24, B-25** (#61) — `--judge-timeout`; one `EntityKey`; dead code removed;
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
