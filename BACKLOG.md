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
6. ~~**v1.0 artifacts (B-31–B-33).**~~ Done. The flagship ran and is published as
   inconclusive (`docs/bench/flagship-results.md`). Next, post-v1.0: a conclusive
   flagship (B-56).

Needs the user: B-35 (catalog submission status).

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

## Review 2026-10-14

An external code, security and maintainability review of `e1a6d5e`, kept
verbatim in `docs/reviews/2026-10-14-code-review.md`, which has the evidence,
the change asked for and the validation for each finding. Its IDs (R01–R38,
C01–C16) are stable: reference them in commits and PRs like backlog IDs
(`Closes R03`). Each finding is re-checked against the code before it is
fixed; a finding that does not hold is marked so here, with the reason.

**P1** — before relying on scores, costs, security or bench results:

- [x] **R01** — npm advisories: triage the audit, update what is compatible, add
  an audit step to CI. Overlaps B-21. Done: the compatible fixes are applied
  (`@grafana/scenes` 8.21, `@grafana/sign-plugin` 3.3.3, two build tools), and
  CI fails on any critical advisory. Of the rest, the 11 in runtime
  dependencies are B-21 (none is in `dist/`: its source maps hold only scenes,
  i18n and uuid); the others are dev tooling — jest's tree (tests only), and
  `@grafana/sign-plugin`'s proxy-agent chain, reached only through a PAC or FTP
  proxy, which the release job does not configure. Clearing those needs jest
  30 and sign-plugin 2.x, both breaking.
- [x] **R02** — `rules.Collector` keeps per-service state for every service
  name ever seen: no admission limit, no expiry. Done in #108: at most 2000 services (`CollectorOptions.MaxServices`); past it, observations are dropped and counted (`rejected_observations`, a report note); a service with no outcome left is dropped.
- [x] **R03** — a CEL evaluation error on an item rule leaves the rule passing:
  the error is dropped and the score can be 100. Done in #108: a failed evaluation counts as a violation, and `eval_errors` is on every rule result.
- [x] **R04** — collector observations and violations are lifetime counters:
  one old violation fails a service forever in `serve`, and aggregate ticks are
  counted again on every export. Done in #108: item outcomes are windowed like the trackers, and each aggregate export replaces the last evaluation.
- [x] **R05** — the Mimir cardinality client reads a field the API does not
  return (`distinct_label_values_count`); its test fixture invents it. Done in #109: `label_values_count`, with fixtures from the documented response; a listed label without its count is an error, not 0.
- [x] **R06** — cost attribution uses Mimir's default top-20 label values and
  in-memory counting, so larger fleets lose services silently. Done in #109: `count_method=active`, `limit=500`, and the remainder past the list and series without the label attributed to named lines, so the total is complete. The service label is now set per backend: `--mimir-service-label` (default `job`) and `--loki-service-label` (default `service_name`); `--service-label` still sets both, deprecated.
- [x] **R07** — LLM redaction strips evidence values but sends the service name
  and the rendered patch, both telemetry-derived. Done in #110: redaction replaces the service name, and sends a patch rendered without telemetry values (`RedactedPatch`); a test checks the whole outbound request.
- [x] **R08** — remediation templates interpolate raw metric and attribute
  names into OTTL strings, and the high-cardinality patch is not scoped to the
  service. Done in #110: names reach configuration only if config-safe, else `REPLACE_WITH_…`; comments quote values on one line; metric-changing patches are scoped to `service.name`.
- [x] **R09** — zero or negative step, stride and interval values hang replay
  or panic `serve` (`time.NewTicker`). Done in #111: every duration flag is checked at command start (`serve` can no longer reach `time.NewTicker(0)`); the backtest entry points refuse a non-positive step, and a walk of more than 100,000 evaluations, before any query.
- [x] **R10** — bench cleanup errors are discarded; a failed restore leaves the
  next repeat on a contaminated cluster. Done in #112: each run records `cleanup_error` and keeps its result; `Finished` is set after cleanup; a failed cleanup stops the run unless the probe verifies the next baseline; a failed last cleanup writes the report and fails the command (`ErrNotRestored`).
- [x] **R11** — reset sweeps argus-managed faults cluster-wide, so parallel
  runners delete each other's faults. Done in #112: a run claims the cluster with a lock ConfigMap (`argus-bench-lock` in `default`, created atomically, outside the sweep's label) and releases it at the end, so a second run fails at once rather than deleting the first one's fault. The sweep stays cluster-wide under the lock.
- [x] **R12** — the Helm chart and the demo compose start `serve` without OTLP
  ingest, expose an unimplemented OTLP HTTP port, and health-check with
  `argus version`. Partly done in #113: Helm starts `--otlp-grpc` and exposes only it (no OTLP/HTTP port); compose runs ingest, health-checks with `argus healthcheck`, binds every port to localhost and pins `otel-lgtm`. The demo is completed in #116: Grafana is built with the plugin, provisioned against the engine, and its Collector mirrors every signal to the engine, which scores its own telemetry from the start (checked end to end with Docker). The image publishing part is B-61.

**P2** — correctness, availability and privacy:

- [x] **R13** — the plugin proxy times out at 10s while a backtest replay can
  take minutes, and the cancelled request prevents the cache from filling. Done in #115: a computation runs detached from the request, shared by concurrent callers and bounded by its own timeout; a request waits 5 s, then gets 503 with Retry-After while it goes on; the plugin pages show "computing" and ask again.
- [x] **R14** — `argus score`'s 60s default never completes a trace window, so
  the trace rules are skipped while reported as evaluated. Done in #118: `argus score` closes the trace window when collection stops, so a short run judges the traces it saw.
- [x] **R15** — the score gate passes an empty fleet (score 100). Done in #108: `--fail-below-score` exits 2 when no service was observed.
- [x] **R16** — attribute values are truncated to 256 bytes before cardinality
  hashing and service identity. Done in #118: items keep full values (sketches and rules see what was sent); evidence is bounded and copied (32 attributes, 256-byte keys, values and summaries, cut on a UTF-8 boundary); long service names become a prefix plus a hash, the same in scores and the service graph.
- [x] **R17** — trace cap overflow can give a zero denominator (an infinite
  ratio JSON cannot encode) and a random set of graph edges. Done in #118: services past the per-trace cap record no parent references, a service with no counted trace gets no ratio, and past the edge cap the busiest edges are kept deterministically, with the count left out in `edges_omitted`.
- [x] **R18** — cardinality verification keeps one outcome per service and rule,
  so a passing pair can overwrite a failing one; queries are not service-scoped. Done in #109: distinct pairs, queries scoped to the service's `job`, one combined result per service. OTLP names are translated to Mimir's: the cardinality rows record unit and type, and the poller matches the metric with or without the unit and type suffixes, and the attribute as a label name.
- [x] **R19** — `PriceFindings` is not called on the score or serve paths, and
  reads verified quantities from the wrong key. Done in #109: `argus score --pricing` and `serve --cost-pricing` price findings; the pricer reads `details.poller.pairs`; the Scores page shows the amount and currency.
- [x] **R20** — the week-over-week trend compares against the latest earlier
  snapshot, not one a week old. Done in #120: the trend compares against the latest snapshot at least a week old and records `baseline_at` (shown on the Spend page); no week-old or same-currency baseline gives no trend, with a note. The team tie-break was #108.
- [x] **R21** — recording-rule synthesis at the root, and cycles. Done in #106
  (B-60). Still open from R21: recording-rule output labels and `offset`/`@`.
- [x] **R22** — `backtest diff` compares total coverage only: different windows
  with the same covered duration are accepted, and zero coverage passes. Done in #119: `backtest diff` maps presence once and replays both sets over those segments; `DiffReports` refuses different windows, steps or segments, and zero coverage; each set's caveats are carried into the diff.
- [x] **R23** — the presence probe (`target_info`) shows backend availability,
  not coverage of each rule's input series. Done in #119: every report states that coverage is measured backend-wide, not per rule; a NaN-only probe answer is not presence (zero still is). Per-rule coverage stays out of scope.
- [x] **R24** — replay ignores a rule's static labels, group interval and
  `keep_firing_for`. Done in #119: `keep_firing_for` is replayed; an alert name in two groups is scored as `group/alert`; a group interval other than the replay step is disclosed; static labels are documented as not applied.
- [x] **R25** — the engine API is open when `ARGUS_API_TOKEN` is unset, and
  `--addr` defaults to every interface. Done in #113: without a token, `serve` refuses a non-loopback `--addr` unless `--insecure-no-auth` is given (kind and the demo pass it, with a comment).
- [x] **R26** — the OTLP receiver's trust boundary (no auth, network policy
  allows every port) is undocumented. Done in #113: the trust boundary is documented (`docs/cli/serve.md`); the chart's NetworkPolicy opens the API and OTLP ports to separate namespace lists, and an empty OTLP list renders no rule rather than an open one; the receiver caps message size and concurrent streams explicitly.
- [x] **R27** — an `https://` self-telemetry endpoint is sent in plaintext. Done in #114: the scheme chooses the transport (`https://` is TLS), `OTEL_EXPORTER_OTLP_INSECURE=true` forces plaintext, and a failed setup shuts down what it started.
- [x] **R28** — HTTP clients follow redirects with custom credentials
  (`x-api-key`, `X-Scope-OrgID`). Done in #114: every engine client comes from `internal/httpclient.New`, which refuses redirects, and so does the plugin backend's; the plugin refuses engine URLs with credentials, a query or a fragment.
- [x] **R29** — unbounded response decoding and subprocess output capture. Done in #111: backend and model responses are read through `internal/bounded` (an error past the limit, never a cut prefix); subprocess output is captured in capped buffers; a tool answer over 256 KiB is cut before the model sees it and logged as `cut`. Still open: redacting query text in bench reports.
- [x] **R30** — the MCP transport and tool arguments need size and range limits. Done in #111: line framing capped at 1 MiB; a bad line costs one message; `jsonrpc` must be "2.0"; cancellation interrupts a blocked read; no trailing data in arguments; ranges, steps, points and limits checked before the backend. Request cancellation is documented as unsupported (requests are sequential).
- [x] **R31** — an empty snapshot sends `services: null`, which crashes the
  plugin's Overview and Scores pages. Done in #108 (engine) and #115 (plugin): empty lists encode as `[]`, and the Overview and Scores pages say no service was observed instead of showing a vacuous score or a success message.
- [x] **R32** — the service graph measures its width before it has mounted. Done in #115: a callback ref with a ResizeObserver measures the graph container when it mounts and follows layout changes.
- [x] **R33** — plugin pages never refresh, a failed comparison hides the Bench
  page, and settings errors only reach the console. Done in #115: every page refreshes every minute, says when it was updated and when a refresh failed (keeping the last data), and ignores stale or late responses; a failed Bench comparison is shown beside the picker and the leaderboard stays; settings show why a save failed; an empty engine URL saves as the default.
- [x] **R34** — `stats.Percentile` rounds where nearest-rank takes the ceiling,
  and NaN passes range checks. Done in #117: `Percentile` takes the ceiling of the rank; pricing, scenario weights, steady-state bounds and synth specs reject NaN and infinity; synth specs also reject duplicate services and incident IDs, negative rates, jitter of 1 or more, and an explicit non-positive step.
- [x] **R35** — `bench-matrix.sh` loses quoted agent flags, and port-forwards
  ignore `ARGUS_KUBE_CONTEXT`. Done in #121: agent flags are split like shell words (quotes kept, nothing run); duplicate labels, unbalanced quotes and non-numeric budgets are rejected; port-forwards use `ARGUS_KUBE_CONTEXT` and every one must answer before a cell starts.
- [x] **R36** — bench provenance does not hash referenced scripts or manifests,
  the tool surface, or the judge config. Done in #121: the scenario hash covers referenced manifests and scripts; reports record the harness (build, injector, tools, normalizers, judge, shell command) and the leaderboard flags differing harnesses; the matrix env digest is the cluster context and version, not a date; the seed is documented as a label.
- [x] **R37** — reports are written in place (a partial file looks complete),
  and `fileSafe` maps `a/b` and `a_b` to the same patch file. Done in #110: artifacts are written atomically (`internal/atomicfile`), and `fileSafe` adds a hash when it changes a name. The matrix half is done in #121: a cell counts as done only if its report matches the cell's condition, repeats and budget.
- [x] **R38** — CI never runs the plugin's Go tests, Playwright, the Postgres
  integration tests, or a formatter check. Partly done in #115: CI runs the plugin backend's Go tests (with -race), the typecheck, a Prettier check, and the Postgres integration tests (testcontainers); `test:ci` no longer passes with no tests; `make test` runs the plugin backend's tests. The Playwright job is added in #122: Grafana with the built plugin (frontend and backend), every page tested against a mocked engine.

**Cleanups** (C01–C16, the table in the review): Prettier on six plugin files (done, #115),
comment trimming (C02, C03, done), stale phase claims (C04, done),
`errors.Is(err, io.EOF)` in the rules loader (done, #108), template parsing once (done, #110), the
numeric helper in remediate (done, #110), one shared clip helper for shortened messages (C09, done), deterministic float sums (C08, done: the trend sort in #108, the totals in #124), typed API responses (C10, done, #124), the settings form's save state (C11, done, #115),
pinned release tools (C13, done), narrower gosec exclusions (C14, done), the Playwright config's scaffold comments (C12, done, #122), `stats.Max` in one scan (C15, done, #117), and client ownership (C16, done, #124).

---

## Phase 4 scope still open

Not defects. Tracked here so nothing is lost between sessions.

- [ ] **B-32** — The LLM judge (the fallback normalizer for shell agents) needs a
  model other than the agent's; the guard refuses the same model id.
- [x] **B-61** — No engine image is published: releases ship the CLI and the
  plugin, and the Helm chart points at `ghcr.io/tamen25/argus`, which does not
  exist. Add an image build to the release workflow (multi-arch, pushed to
  GHCR), so `helm install` works without building one. Found with R12. Done:
  a release tag pushes `ghcr.io/tamen25/argus:X.Y.Z` for amd64 and arm64 (the
  Dockerfile cross-compiles, so no step runs under emulation). The first image
  comes with the next release; v1.0.0 has none.
- [ ] **B-56** — A conclusive flagship. v1.0's agent scored at the floor under
  both conditions (`docs/bench/flagship-results.md`). Needs an agent that
  diagnoses some of these incidents, more passes (`--pass N`), and likely a
  larger token budget: 7 of 8 unanswered runs ended at the 100k cap.
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

- [x] **B-33** (#98) — the flagship: report generator (#74), conditions (#81),
  Bench page (#82), matrix runner (#85, #89), and the v1.0 run, published as
  inconclusive in `docs/bench/flagship-results.md` (one pass, `gpt-oss:20b`,
  Δ +0.17 over 3 paired scenarios at the score floor).
- [x] **B-55** (#97) — found by the second flagship pass: every answered run
  scored 0 because no citation was in the schema's shape, so the evidence rule
  zeroed runs that named the right workload and category. `submit_diagnosis`
  now checks its arguments (parse, fields, offered category, one well-formed
  citation) and answers a problem with a tool error, at most twice per run;
  counted (`submit_rejections`) and disclosed. The rubric is unchanged.
- [x] **B-54** (#96) — found by the second flagship session: a single HTTP 500
  from the model endpoint (a tool call the server could not parse) ended the
  run. Model calls are now retried on a 5xx, at most twice per run; counted
  (`retries`) and disclosed by `--compare`.
- [x] **B-53** (#95) — found by the second flagship session: `ParseDiagnosis`
  decoded evidence strictly, so a citation in the agent's own shape failed the
  whole diagnosis, which then dropped out of the agent's average (the outcome
  `Validate` documents as ruled out). Evidence items now decode leniently and
  are filtered by `WellFormed`; the rest of the diagnosis stays strict. Also
  (#93): failed runs keep the last reply or the rejected submission, and the
  budget notices no longer read as forbidding `submit_diagnosis`.
- [x] **B-52** (#94) — found by the second flagship session: a scenario's reset
  sweep ran `kubectl -n <ns> delete <kind> --all-namespaces`, which kubectl
  rejects whenever an object matches, so a leftover fault failed the next
  scenario's reset instead of being swept. The sweep now passes no namespace.
- [x] **B-51** (#92) — found by the first flagship pass: 7 of 16 runs ended because
  the model replied in prose instead of calling `submit_diagnosis`, and the API
  adapters failed a run on the first such turn. Both now remind the agent once
  (not past the token cap); the reminder is counted (`nudges`) and `bench
  report` discloses how many answered runs needed one.
- [x] **B-50** (#91) — found by the first flagship session: `apply.sh` proved the
  remediated condition as soon as one service carried a Kubernetes identity, so
  the first two remediated runs started with 1 to 10 of 17 services covered.
  It now requires 90% of the services reporting `target_info`, and waits up to
  15 minutes. Those two cells were rerun.
- [x] **B-49** (#90) — found by the first flagship session: the OpenAI adapter sent
  no per-turn output cap, and the token budget is only checked between turns, so
  a model looping in its reasoning held one turn open for the full 30-minute
  timeout. `--max-turn-tokens` (default 8192) now caps every turn for both API
  adapters, a turn cut off at the cap ends the run saying so, and the cap is
  recorded (`max_turn_tokens`) and caveated by `--compare` when it differs.
- [x] **B-48** (#88) — found by the flagship run: an interrupted `bench run`
  left its fault in the cluster (cleanup was handed the cancelled context), and
  the report did not record the agent timeout that had ended three runs. Now
  SIGINT/SIGTERM cancel the run, cleanup runs on a detached context with its own
  limit, no partial report is written, and `agent_timeout` is recorded and
  caveated by `--compare` when it differs.
- [x] **B-60** (#106) — found reviewing `internal/backtest`: (1) burn-rate
  windows lost a tens digit when rendered: 30m became `[3]`, which Prometheus
  reads as 3 seconds, so the default slow-burn alert was replayed over a 3s
  window and almost never fired (90s and 90m broke the same way);
  (2) `Synthesize` treated a bare recording rule at the root of an expression
  differently: rules it referenced were left in, and an undefined one passed
  silently; (3) recording rules defined in terms of each other recursed until
  the stack overflowed.
- [x] **B-59** (#103) — found reviewing `cmd/argus`: (1) `cost`, `backtest`,
  `calibrate` and `remediate` printed reports with cobra's `cmd.Print`, which
  writes to **stderr**, so `argus cost > report.md` produced an empty file;
  (2) `score --help` promised exit code 2 for a tripped gate, but every error
  exited 1; now every CI gate exits 2 and any other error 1; (3) `remediate`
  named patch files after the service, which comes from telemetry, so a
  service named `../x` could write outside `--out`.
- [x] **B-58** (#102) — found reviewing `internal/ingest`: exemplar coverage
  (ARG-MET-001) was counted for the engine's lifetime, so in `argus serve` one
  exemplar at startup passed the rule forever and an exemplar bridge that broke
  later was never caught. The counts are windowed like the sketches now.
- [x] **B-57** (#100) — security review. Found: the engine never checked the
  API token the Helm chart generates and §5.3 requires, so a "token-protected"
  API was open to anything that could reach it. Now `/api/` requires
  `ARGUS_API_TOKEN` as a bearer token and the plugin sends it from an
  encrypted setting. Hardening: GET-only routes, server timeouts, a JSON writer
  that cannot send half a 200, MCP label/tag and namespace names validated, the
  rules loader confined to its directory with `os.Root`, plugin engine URL
  validated and 502s that do not leak it, gosec as a lint gate.
- [x] **B-46** (#87) — the flagship agent: one open-weights model on a server
  the maintainer runs, reached through the OpenAI-compatible adapter; no hosted
  APIs (DECISIONS.md, 2026-10-09).
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
