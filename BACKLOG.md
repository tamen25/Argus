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

## P0 — PR #61 must not merge until these are fixed

Found by code review of #61 (2026-09-30). Each silently corrupts a bench score.

- [ ] **B-01** — The LLM judge never extracts `evidence` (`judge.go` `judgeShape`),
  so under `requireEvidence: true` every prose/shell agent scores 0 whatever it
  answered. That silently zeroes HolmesGPT, one of the three adapters.
- [ ] **B-02** — One `PromQLProbe` is shared across repeats and `firstHeld`
  survives between them, so repeat 2 onward skips the settle window. It is also
  not reset on an empty result or backend error.
- [ ] **B-03** — Scenario 1's gate cannot tell repeat N from N-1. The frontend
  OTel SDK keeps cumulative attribute sets in memory after the driver is deleted,
  so `count(...) >= 300` passes before the new fault acts. Repeats are not
  independent. Needs a reset that restarts frontend (see B-10).
- [ ] **B-04** — One malformed evidence item fails `Diagnosis.Validate`, so the run
  drops out of the mean instead of scoring 0. Citing garbage *raises* an agent's
  average.
- [ ] **B-05** — The judge≠agent guard compares tag strings. `qwen3.6-bench` and
  `qwen3.6:35b-a3b-q4_K_M` share weights and pass it. Compare the weights digest
  from `/api/show`.
- [ ] **B-06** — `--local-only` defaults on, which blocks the master plan's
  remote-API use case for every other user. Make it opt-in; the maintainer's
  no-paid-API guarantee is set explicitly in their own runs. Decided 2026-09-30.

## P0 — Security

- [ ] **B-07** — govulncheck: **18 reachable vulnerabilities**, all fixed by version
  bumps. stdlib ×14 → go1.26.6 (html/template XSS ×3, HTTP/2 infinite loop,
  crypto/tls, asn1/xml recursion); `google.golang.org/grpc` ×3 → v1.83.1 (incl.
  HTTP/2 DATA-frame OOM); `golang.org/x/text` ×1 → v0.39.0. Add a `toolchain`
  directive: stdlib vulns follow the *build* toolchain, and there is none today.
- [ ] **B-08** — CI toolchain is unpinned (`go-version: stable`, golangci-lint
  `latest`), so an upstream release can fail CI with no code change. Pin both,
  add govulncheck. (A local golangci-lint 2.5.0 already panics on Go 1.26.)

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
- [ ] **B-14** — Correct "checkout emits only 10 series". That was read on a
  10-minute-old cluster; after traffic, checkout emits `rpc_*`. Retargeting to
  frontend was still right, but because checkout's rpc labels are bounded
  (`rpc_method=PlaceOrder`). Fix DECISIONS, docs, the manifest and scenario
  headers, and the #61 body.
- [ ] **B-15** — `calibration_test.go` still models the deleted checkout scenario;
  load the real YAML. The stub defaults `Obvious` to checkout, and the `cited`
  profile's fabricated query names checkout.

## P1 — Infrastructure

- [ ] **B-16** — The kind history bind mount **detaches on every Docker Desktop
  restart** that beats the Ubuntu distro up (seen 2026-07-25 and 2026-09-30).
  Docker substitutes an empty root-owned dir; MinIO crashloops and takes Mimir
  with it. Crashing is the *lucky* outcome: a writable empty dir would silently
  fork the history kept since Phase 0. Add a sentinel file, a preflight that
  refuses to start without it, and `make dev-heal`.
- [ ] **B-17** — `make dev-up` does not deploy argus, contrary to the Makefile
  comment and CLAUDE.md. `deploy/kind/argus-engine.yaml` exists, but
  `bootstrap.sh` never applies it.

## P2 — Quality gates

- [ ] **B-18** — CLAUDE.md's ≥70% coverage bar is not enforced anywhere. All gated
  packages pass today (rules 89.3, cost 90.7, backtest 83.5, bench/scoring 96.6),
  so a CI gate costs nothing to add.
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
- [ ] **B-24** — `@stylistic/eslint-plugin-ts` is deprecated; migrate to
  `@stylistic/eslint-plugin`.

## P3 — Cleanup

- [ ] **B-25** — The judge's timeout is wired to `agentTimeout` (10 m), so
  `DefaultJudgeTimeout` (5 m) is dead on the CLI path, and there's no
  `--judge-timeout`.
- [ ] **B-26** — `bench.entityKey` duplicates `scoring.key()`, synced only by a
  comment; export one. Dead code: `agent.SortedProfiles`, and
  `Summary.EvidenceMissingRate`, which is computed but never rendered.
- [ ] **B-27** — The `shotgun` stub profile names generic services, so via the CLI
  it never hits a scenario's decoys.
- [ ] **B-28** — ~19 stale remote branches from July. They were squash-merged, so
  `git branch --merged` reports them all unmerged; check each PR's state before
  deleting.
- [ ] **B-29** — After a compactor crashloop (B-16), Mimir's bucket index goes
  stale (>1 h), and label/metadata APIs error until the compactor catches up.
  Instant queries still work. Add to the dev runbook.

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

_Nothing yet — items move here with their PR number._
