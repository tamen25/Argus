# Argus code, security, and maintainability review

Reviewed on 14 October 2026, against commit `e1a6d5e` and the current working tree.

This is a review only. The changes below are recommendations, not implemented fixes. Existing source, tests, configuration, and lockfiles were left unchanged. Additional regression probes, scanner binaries, build output, and coverage output were placed in the Windows temporary directory; Go overlays allowed the probes to run without adding test files to the checkout.

## Scope and priorities

The review covered the engine's CLI and server, ingestion and rule evaluation, cost and backtest logic, benchmark orchestration and agents, MCP tools, Grafana frontend and backend, deployment templates, and CI/release scripts. Existing tests and the project instructions were used to distinguish deliberate behavior from defects. The pre-existing untracked `deploy/terraform/bench-vm/` directory was left alone; it contains Terraform working artifacts, not a tracked implementation reviewed here.

- **P1:** Fix before relying on production security, scores, costs, or benchmark results.
- **P2:** Correctness, availability, privacy, and test gaps that deserve focused fixes.
- **P3:** Readability and maintenance improvements.
- **Reproduced:** A temporary regression probe exercised the defect in this checkout.
- **Confirmed by inspection:** The relevant implementation establishes the behavior; a dedicated runtime reproduction was not performed.
- **Contract mismatch:** Local code was compared with the upstream API documentation. No live backend was queried to reproduce it.

This is a risk-focused source review, not a claim that every possible defect has been found. Infrastructure changes, real model calls, live fault injection, and deployment were outside this review.

## Checks actually run

| Check | Result |
| --- | --- |
| Engine `go test -mod=readonly -cover ./...` | Failed in three shell-dependent orchestrator tests on Windows; the other package test results passed. Some packages used Go's existing test cache. |
| Plugin backend `go test -mod=readonly ./pkg/...` | Passed using a temporary build cache. The default cache initially returned access-denied errors. |
| Frontend `npm run test:ci -- --no-cache --coverage --coverageDirectory=<temporary directory>` | 9 suites, 27 tests passed. Statement coverage: 349/370, **94.32%**. Branch coverage: 151/219, **68.95%**. These figures describe the files included by the current Jest configuration, not all repository code. |
| Frontend `npm run typecheck` | Passed. |
| Frontend `npm run lint -- --no-cache` | Passed, with a deprecation notice for `@stylistic/eslint-plugin-ts`. |
| Prettier check of `plugin/src` | Failed: six files have formatting differences, listed below. |
| `gofmt -l` of engine and plugin backend Go files | No formatting differences reported. |
| Engine `go vet -mod=readonly ./...` | Passed. |
| Engine build, output in temporary directory | Passed. |
| `govulncheck` v1.8.0, engine | No reachable vulnerabilities found. It also reported one vulnerability in imported packages and two in required modules that the code did not appear to call. These counts should not be added together as distinct findings. |
| `govulncheck` v1.8.0, plugin backend | No vulnerabilities found. |
| `npm audit --json` | 67 affected package entries: 54 high, 13 moderate, 0 critical. There are **24 distinct advisories** behind those entries; transitive effects account for much of the package count. |
| Six additional temporary regression probes | All six failed against the intended corrected behavior, confirming R02, R03, R04, R07, R21, and R22. |

The three engine failures were `TestAutoInjectorResetStopsTheSourceFirst`, `TestAutoInjectorCleanupKeepsGoing`, and `TestAutoInjectorDispatchesByStepType`. `bash` resolves to the Windows WSL launcher in this environment, and the shell subprocesses exited with status 1. A separate WSL check returned `Wsl/Service/CreateInstance/E_ACCESSDENIED`. Treat this as an environment-dependent validation failure, not evidence that Linux CI fails.

Race detection and golangci-lint were not run: Windows has `CGO_ENABLED=0`, the project requires those checks in WSL, and WSL was inaccessible. Docker/Postgres integration tests, Playwright against Grafana, frontend production bundling, Helm rendering, and a live backend compatibility run were not performed. Existing CI definitions are not evidence that those checks passed during this review.

### Coverage observations

| Engine package | Statement coverage reported |
| --- | ---: |
| `internal/rules` | 92.1% |
| `internal/cost` | 91.4% |
| `internal/backtest` | 83.5% |
| `internal/bench/scoring` | 96.4% |
| `internal/ingest` | 91.5% |
| `internal/mcp` | 83.4% |
| `internal/mcp/backend` | 82.5% |
| `internal/model` | 71.7% |
| `internal/synthhist` | 60.4% |
| `internal/store` | 0.0% in the ordinary unit-test run; its integration tests were not run |

All four packages named by the existing 70% coverage gate exceed the threshold. The defects below show why more percentage alone is insufficient: tests need additional boundary cases, recovery transitions, and real API fixtures.

## P1 findings

### R01 — Triage and update the npm dependencies with security advisories

**Evidence:** `plugin/package-lock.json`, the installed dependency manifests, and the audit run described above. Confirmed by scanner, with application reachability still to be assessed.

Examples in the installed tree include `source-map-js` 1.2.1, `basic-ftp` 5.3.1, `js-cookie` 2.2.1, `braces` 3.0.3, DOMPurify 3.3.2, Moment 2.30.1, and React Router 6.30.6. The audit associates these with denial-of-service, sanitization, cookie handling, locale handling, or routing advisories. The high-severity source-map denial-of-service advisory and cookie-attribute injection advisory are documented in the [source-map-js advisory](https://github.com/advisories/GHSA-68fv-2mgg-jv7q) and [js-cookie advisory](https://github.com/advisories/GHSA-qjx8-664m-686j).

**Change needed:** Review the full audit dependency paths, update compatible direct and transitive dependencies, and track any advisory requiring an upstream Grafana update. Assess build-time dependencies separately from browser code and host-provided libraries: `.config/bundler/externals.ts` externalizes several Grafana, React, Moment, and routing dependencies. An audit entry does not prove an exploitable path in this plugin. Avoid a blanket `npm audit fix --force` or unsupported global overrides. Add an npm dependency audit to CI, with explicit, narrow exceptions for unresolved upstream issues.

**Validation needed:** Fresh lockfile installation, audit, typecheck, unit tests, production build, and plugin smoke tests on the supported Grafana version.

### R02 — The collector has no bound on distinct services

**Evidence:** `engine/internal/rules/collector.go:62`, `:92`, and `:162`. Reproduced: a temporary probe retained all 5,000 distinct service names.

Sketch and trace generations have caps, but `Collector.svc` and `Collector.poller` grow for every new service and never expire. Each service creates several maps and may retain evidence. An OTLP sender can keep introducing service names and exhaust memory. The comment describing the collector as bounded is therefore incomplete.

**Change needed:** Add a service admission limit and expiry tied to observation windows, including poller state. Report evictions or rejected admissions. Account for all trackers together when defining a memory budget: `MaxPairs` applies independently to four sketch trackers, and traces have a separate allocation envelope.

**Validation needed:** Service churn across many windows, idle expiry, concurrent snapshots, and a retained-memory test under adversarial service/attribute cardinality. Keep the existing steady-state allocation test, but do not use it as proof of a heap bound: it repeatedly feeds the same small payload.

### R03 — Item-rule evaluation errors can produce a perfect score

**Evidence:** `engine/internal/rules/collector.go:107` and `:229`. Reproduced with a rule that reads a missing span attribute.

`ObserveItem` increments observations but records CEL errors only in `evalErrs`. The violation count stays zero, so `serviceReport` marks the rule passed. Error details are attached only to a failing finding; with no finding, the error disappears from the report. The probe produced a score of 100 with no findings despite the evaluation error. Aggregate evaluation errors follow a different path and are counted as ordinary violations without recording their cause.

**Change needed:** Represent evaluation failures explicitly and apply one consistent policy to item and aggregate rules. An unevaluated rule must not silently become a successful check. Surface the error count in the report independently of whether the rule failed.

**Validation needed:** Missing keys, unexpected attribute types, invalid aggregate fields, mixed successful/failed evaluations, and score-gate behavior when evaluation is incomplete.

### R04 — Live scores accumulate old violations indefinitely

**Evidence:** `engine/internal/rules/collector.go:107`, `:131`, `:229`; `engine/internal/ingest/pipeline.go:152`; `engine/cmd/argus/serve.go:281`. Reproduced for aggregate recovery.

Trackers rotate, but collector observations and violations are lifetime counters. With the default zero threshold, one violation keeps a service failed after subsequent windows become healthy. Item evidence keeps the first samples forever. Repeated aggregate export ticks count the same state repeatedly, making ratios depend on scrape/export frequency. Reports nevertheless advertise a particular window and current findings.

**Change needed:** Keep report state for the declared observation window. Replace aggregate outcomes for a completed evaluation rather than treating every export tick as an independent observation. Expire outcomes for rows/services absent from the new window, and retain recent evidence.

**Validation needed:** Bad-to-good and good-to-bad transitions, disappearance of an aggregate, idle services, changed export intervals, and removal of stale evidence after rotation.

### R05 — The Mimir distinct-value response field is wrong

**Evidence:** `engine/internal/ingest/mimir/client.go:45`; `engine/internal/ingest/mimir/client_test.go`, `TestLabelCardinality`. Contract mismatch.

The adapter reads `distinct_label_values_count`, and its test invents that field. Mimir documents `labels[].label_values_count`. A documented response consequently decodes to zero and verification leaves sampled findings unchanged. See the [Mimir label-values cardinality API](https://grafana.com/docs/mimir/latest/references/http-api/#label-values-cardinality).

**Change needed:** Use the documented field and replace the invented fixture with a captured or documentation-based response. Distinguish a missing/malformed required field from a measured zero.

**Validation needed:** Contract fixtures plus an integration check against the deployed Mimir version, including no matching label and malformed responses.

### R06 — Cost attribution silently uses only Mimir's top label values

**Evidence:** `engine/internal/ingest/mimir/client.go:71`; `engine/internal/cost/sources.go`. Contract mismatch.

`SeriesCountByLabel` does not set `limit`, request active counting, or detect omitted values. The documented default returns only 20 cardinality entries and defaults to in-memory counting. Larger fleets therefore lose services from the cost report, while an in-memory count is labeled active series. Raising the limit alone still needs an omission check. See the [Mimir API parameters and response totals](https://grafana.com/docs/mimir/latest/references/http-api/#label-values-cardinality).

**Change needed:** Request the intended counting method, reconcile the returned entries with response totals, and disclose or reject partial attribution. Use a supported complete attribution strategy when the endpoint's maximum limit is insufficient. Validate the service label for the actual metrics ingestion path.

**Validation needed:** More than 20 and more than 500 service values, inactive series, missing service labels, and reconciliation of attributed counts with backend totals.

### R07 — Default LLM redaction leaks values through other input fields

**Evidence:** `engine/internal/remediate/llm/llm.go:51`; `engine/cmd/argus/remediate.go`, `explainInputFor`; `engine/internal/remediate/llm/prompts/explain.tmpl`. Reproduced.

`Redact` removes evidence values and summaries, but preserves `Service` and `Patch`. The service name is telemetry-derived, and rendered patches contain service, metric, and attribute names. A sensitive `service.name` value remains in the prompt even though its evidence entry is replaced by `<redacted>`. Calling these fields Argus-owned structural text hides the actual privacy boundary.

**Change needed:** Build a dedicated safe explanation input. Replace telemetry-derived values consistently throughout service identity, patches, and comments before an external call. Preserve the real deterministic patch locally; send the explanation model only the information permitted by the redaction setting.

**Validation needed:** Inspect the complete outbound request for unique sensitive markers placed in every input field, rendered patch, and evidence entry. Test the default path and explicit opt-out separately.

### R08 — Remediation interpolation is unsafe and the patch can affect other services

**Evidence:** `engine/internal/remediate/remediate.go:88`; `engine/internal/remediate/templates/high-cardinality-attribute.yaml.tmpl` and `.river.tmpl`.

Raw metric and attribute names are inserted into quoted OTTL expressions. Quotes, backslashes, line breaks, or River raw-string delimiters can break the generated syntax or change its meaning. The sanitized service name only protects block identifiers. The high-cardinality patch also filters on metric name without restricting `resource.service.name`, so applying it to a shared collector can remove the attribute from healthy services emitting the same metric.

**Change needed:** Encode values for each output language and scope the generated transformation to the affected service and metric. Treat comment interpolation separately so telemetry cannot create additional lines or config blocks. For a service with unknown identity, require explicit scoping guidance rather than guessing.

**Validation needed:** Parse rendered Collector YAML and Alloy configuration containing hostile/unusual names. Verify transformation behavior on two services sharing a metric, with only one service affected. Keep human review before application.

### R09 — Invalid durations can hang replay or crash serving

**Evidence:** `engine/internal/backtest/run.go:27`, `segments.go`, `replay.go`, `window.go`; `engine/cmd/argus/backtest_run.go`; `engine/cmd/argus/serve.go:237`.

Backtest step/stride values are not checked before time-stepping loops. Zero or negative values can prevent forward progress and repeatedly query a backend. A non-positive score interval reaches `time.NewTicker` in a goroutine and panics. Several window, grace, and cache durations are accepted without consistent validation.

**Change needed:** Validate public inputs before starting goroutines or making backend calls. Require positive steps/intervals/windows, sensible cache durations, and non-negative attribution margins. Guard lower-level exported replay/probe functions too, since they can be called independently of the CLI. Bound total evaluation work for very small steps over large windows.

**Validation needed:** Zero/negative/sub-second values and excessively large work requests. Assert immediate actionable errors and no backend calls.

### R10 — Benchmark cleanup failures are discarded

**Evidence:** `engine/internal/bench/orchestrator/orchestrator.go:232`, deferred cleanup.

`runOnce` discards `inj.Cleanup` errors, and records `Finished` before cleanup completes. A successful diagnosis can therefore produce a successful-looking report while the fault remains active. Later repeats may continue against contaminated state. The detached cleanup context is useful, but successful invocation is not the same as successful restoration.

**Change needed:** Record cleanup outcomes and completion time on the returned run record. Preserve an existing diagnosis/error while reporting restoration failure. Stop subsequent repeats when the environment cannot be restored safely, or require a verified reset before continuing.

**Validation needed:** Successful diagnosis followed by failed cleanup, diagnosis failure plus cleanup failure, cleanup timeout after cancellation, and prevention of a contaminated next repeat.

### R11 — Reset sweeps faults from other benchmark runs

**Evidence:** `engine/internal/bench/inject/kube/kube.go:78`.

The reset sweep deletes labeled fault objects cluster-wide with `--all-namespaces`, even when an injection namespace is configured. A second runner sharing the cluster can delete the first runner's active fault. That conflicts with the build plan's idea of parallel runs in separate namespaces and changes both results and cluster state.

**Change needed:** Establish explicit run ownership. Scope normal cleanup to its namespace and run label. Keep cluster-wide recovery as an explicit operator action, or enforce a single-run cluster lock if global sweeps remain part of normal operation.

**Validation needed:** Two independent namespaces/runners, ownership labels, stale recovery, and manifests with explicit namespaces. One run's reset must not remove another run's objects.

### R12 — The shipped Helm and Compose paths still deploy a scaffold

**Evidence:** `deploy/helm/argus/templates/deployment.yaml:23`, `templates/service.yaml`, `values.yaml`; `deploy/demo/docker-compose.yml`.

Helm starts `serve --addr` without `--otlp-grpc`, so ingest and the score API routes remain disabled even though the Service exposes OTLP ports. It also exposes OTLP HTTP without an implemented HTTP receiver. Compose starts the same health-only mode, does not install/provision the Argus plugin or wire a mirror, and uses `argus version` as a healthcheck; that succeeds independently of server health. The documented quick demo cannot deliver the advertised product through these definitions.

**Change needed:** Wire the supported receiver and actual capabilities into deployment values, volumes, and arguments. Expose only implemented listeners. Complete plugin provisioning and telemetry fan-out in the demo. Use a real health probe and test custom HTTP listen ports. Retain intentional local demo settings, but bind exposed demo ports to localhost unless broader exposure is requested.

**Validation needed:** Rendered Helm arguments/ports, receiver connectivity, plugin-to-engine authentication, a non-empty score report, and a clean Compose smoke run.

## P2 findings

### R13 — The plugin timeout and cache design can make backtests impossible to load

**Evidence:** `plugin/pkg/plugin/app.go:63`; `engine/cmd/argus/serve_backtest.go:59`; `engine/cmd/argus/cache.go:31`; `engine/cmd/argus/serve.go:426`.

The engine explicitly allows a replay taking minutes, while its Grafana proxy cancels requests after ten seconds. The cache computation uses the requesting context, so that cancellation prevents cache population. Repeated loads restart the expensive work. The cache mutex also blocks other callers without observing their cancellation while they wait.

**Change needed:** Align proxy and operation timeouts, or compute a bounded shared replay independently of any one browser request and serve its result/status. Make waiting cancelable, and define how refresh errors affect the last usable result. Prefer a small, explicit synchronization design over a generic job framework.

**Validation needed:** Replay taking longer than ten seconds, two simultaneous callers, cancellation of one caller, successful reuse by the other, and engine shutdown during replay.

### R14 — Default CLI scoring cannot evaluate completed trace windows

**Evidence:** `engine/cmd/argus/score.go:95`; `engine/internal/ingest/cardinality.go`, `DefaultWindow`; `engine/internal/ingest/tracetracker.go:164`.

The score command collects for 60 seconds by default, creates a pipeline with a one-hour trace window, then requests aggregates. Trace health reads only the previous completed generation. In an ordinary short run, that generation never exists, so orphan/missing-root rules are skipped while the report lists loaded rules as evaluated.

**Change needed:** Define finalization for a finite collection run, including a documented grace period for delayed spans. Keep continuous serving windows distinct from CLI collection semantics. Report the rules actually applicable/evaluated during the run.

**Validation needed:** A short CLI run containing a rootless/orphan trace, delayed parent arrival, and a quiet collection. Verify both findings and evaluated-rule disclosure.

### R15 — The score CI gate passes an empty fleet

**Evidence:** `engine/internal/rules/collector.go`, `Snapshot`; `engine/cmd/argus/score.go`, score threshold handling; `.github/workflows/ci.yml`, dogfood gate.

An empty fleet gets 100. A note is added when an OTLP listener received nothing, but `--fail-below-score` still succeeds. With neither OTLP input nor meaningful poller observations, even the note may be absent. A broken telemetry connection can therefore pass the CI gate.

**Change needed:** Require enough observations for a score gate to be meaningful. Return an explicit insufficient-data failure when a threshold was requested and no services/rules were observed; optionally provide a deliberate CLI override for exploratory use.

**Validation needed:** No input configured, listener with no traffic, failed backend verification, and a healthy non-empty dogfood report.

### R16 — Truncation occurs before identity and cardinality evaluation

**Evidence:** `engine/internal/model/pdata.go:121`, `:140`; `engine/internal/ingest/pipeline.go:104`; `engine/internal/rules/collector.go:306`.

Attribute strings are truncated to 256 bytes before service extraction, CEL evaluation, and HLL insertion. Distinct values sharing that prefix collapse into one cardinality value, and long service names collapse into one service. The trace tracker reads the untruncated service name, producing a different identity from the collector. Evidence summaries retain unbounded span/metric/severity names; maps have no attribute-count/key-length cap. Short string slices can also retain a much larger original backing string.

**Change needed:** Separate full-value evaluation/hashing from bounded evidence storage. Normalize service identity consistently across both paths, apply explicit evidence field/count limits, and copy retained prefixes where necessary. Preserve UTF-8 when truncating display text.

**Validation needed:** Long values with identical prefixes but distinct suffixes, long service names across graph and score output, large attribute maps, unusual UTF-8 boundaries, and retained-memory measurements.

### R17 — Trace cap overflow can produce invalid health ratios and unstable graphs

**Evidence:** `engine/internal/ingest/tracetracker.go:97`, `:164`, `:222`, `:253`.

Services past `maxServicesPerTrace` are still inserted into parent reference sets. `tallyHealth` can count them as orphaned/rootless without counting them as participating traces, because the denominator comes from `svcIdx`. This can yield a zero denominator and infinity, which JSON cannot encode. When the graph edge cap is reached, random Go map iteration determines which edges survive, with no omission disclosure.

**Change needed:** Apply one overflow policy to service indexing and parent references, and never emit a ratio without a valid denominator. Count and disclose per-trace/graph truncation. Choose graph entries deterministically when capped.

**Validation needed:** More than 64 services per trace, more than 512 spans, unresolved parents from overflow services, and more than 4,096 edges. Reports must remain valid and stable.

### R18 — Cardinality verification overwrites earlier failures and lacks service scope

**Evidence:** `engine/internal/ingest/poller.go:90`; `engine/internal/rules/collector.go:162`; `engine/internal/ingest/mimir/client.go:45`.

The collector stores one poller outcome per service/rule, but verification processes multiple metric/attribute pairs and overwrites that outcome each time. A passing pair can erase a failing pair. Duplicate retained evidence also repeats queries. The backend selector includes the metric but no service restriction, so a tenant-wide count can be attributed to each individual service.

**Change needed:** Verify unique scoped pairs, retain pair-specific evidence, and combine outcomes explicitly so any verified failing pair keeps the rule failed. Map OTLP attribute/metric identities to their backend representation rather than assuming identical names.

**Validation needed:** One failing plus one passing pair in both orders, duplicate evidence, and two services with different cardinality for the same metric/attribute.

### R19 — Per-finding pricing is not connected to the product paths

**Evidence:** `engine/internal/cost/finding.go:8`, `:56`; `engine/internal/rules/collector.go`, poller details; `engine/cmd/argus/score.go`; `serve.go`; `plugin/src/components/Scores/ScoresContent.tsx`.

Production score/serve paths do not call `PriceFindings`, and the Scores page does not display the estimated finding cost. In addition, verified quantities are stored in `Details["poller"]`, while `findingQuantity` looks at `Details[field]`; its stated preference for backend quantities is not implemented.

**Change needed:** Connect pricing to the reporting paths intended to support this feature, read the actual verified detail shape, and show the optional amount/currency in the UI. Preserve an unknown quantity as unknown, rather than inventing zero or pricing label cardinality as an exact active-series count.

**Validation needed:** An end-to-end backend-verified finding with a known quantity/rate, a missing quantity, and the corresponding frontend display.

### R20 — Week-over-week trends actually compare with the immediately prior request

**Evidence:** `engine/internal/cost/assemble.go:33`; `engine/internal/store/postgres.go`, `LastCostSnapshot`; `engine/internal/cost/trend.go:27`.

`Assemble` requests the last snapshot before `now`, not before a week-old comparison point. With a one-minute HTTP cache, the page's week-over-week label usually describes a minute-to-minute delta. Currency changes are also compared without a compatibility check. Trend ordering omits the team tie-breaker despite grouping by service, team, and signal.

**Change needed:** Select and disclose the actual baseline time, reject incompatible currencies/rate versions, and sort on the complete grouping key. Keep historical pricing metadata sufficient to explain a delta after configuration changes.

**Validation needed:** Multiple recent snapshots plus a week-old baseline, a missing/very old baseline, changed currency, and two teams sharing a service/signal.

### R21 — Recording-rule synthesis misses root cases and can recurse indefinitely

**Evidence:** `engine/internal/backtest/synth.go:25`. Bare nested recording reference reproduced.

The special root-vector path substitutes only one level: `a:b -> c:d -> up` leaves `(c:d)` unresolved. Undefined external recording references are rejected when visited as children but not consistently at the root. Recursive definitions have no visiting set or depth/work limit. Substitution ignores recording-rule output labels and selector `offset`/`@` semantics, potentially changing the query rather than merely approximating its schedule.

**Change needed:** Use one rewrite path for root and child expressions, detect dependency cycles, and explicitly reject or preserve unsupported label/time semantics. Validate the rewritten AST. Keep fidelity caveats attached to the specific rules affected.

**Validation needed:** Bare/nested references, self/mutual cycles, undefined root references, label-bearing recordings, offsets, `@`, and range selectors. Use a bounded error for cycles rather than a process crash.

### R22 — Backtest comparisons accept different windows with equal coverage

**Evidence:** `engine/internal/backtest/diff.go:41`; `engine/cmd/argus/backtest_diff.go`. Reproduced.

`DiffReports` compares only total covered duration. Different calendar windows or different gaps can have the same duration and be accepted as comparable. The CLI maps presence independently for each rule set, so backend changes between the two sweeps can also alter the evaluated history. Zero-coverage reports can produce a reassuring no-regression result without evidence.

**Change needed:** Share one coverage mapping for a diff invocation. Carry and compare the actual interval/segment identity, step, and relevant evaluation settings; fail a CI comparison with insufficient coverage. Retain per-set synthesis caveats in the diff report.

**Validation needed:** Equal duration with different timestamps/gaps, changed presence between sweeps, zero coverage, and a rule skipped during synthesis.

### R23 — Global presence does not prove coverage for an alert's input series

**Evidence:** `engine/internal/ingest/mimir/eval.go:95`; `engine/internal/backtest/run.go:27`; `engine/internal/backtest/score.go`.

The default presence expression checks fleet-wide `target_info`. An unrelated healthy service can make a segment look covered while the metric/service needed by a rule has no history. Such an incident is then marked missed rather than unverifiable. `HasData` also treats a non-empty zero/NaN-valued result as presence.

**Change needed:** Define usable coverage for the rule's relevant data, or disclose that only global backend availability was measured and avoid claiming per-rule verifiability from it. Specify the semantics expected of custom probe expressions.

**Validation needed:** Healthy unrelated service plus absent target series, stale data, zero/NaN probes, relabeled metrics, and partially covered incidents.

### R24 — Loaded alert semantics are silently dropped during replay

**Evidence:** `engine/internal/backtest/rules.go`, `Rule`/`Group`; `engine/internal/backtest/replay.go`; `engine/internal/backtest/run.go`.

The loader retains static labels and group intervals, but replay ignores both. `keep_firing_for` is accepted by the upstream loader without being represented in Argus's rule model. Alert identity can change when configured labels overwrite result labels, and continued-firing behavior changes resolution/page counts. These omissions need explicit handling beyond the general alignment caveat. The [Prometheus alerting-rule documentation](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/) describes these label and firing controls.

**Change needed:** Implement supported semantics or reject/report unsupported ones per rule. Preserve file/group identity so two groups with the same alert name are not silently conflated in dependency maps or scorecards.

**Validation needed:** Label overrides, `keep_firing_for`, differing group intervals, and duplicate alert/recording names across files.

### R25 — Engine authentication fails open outside Helm

**Evidence:** `engine/cmd/argus/serve.go:177`, default `--addr :8080`; `deploy/demo/docker-compose.yml`.

An absent `ARGUS_API_TOKEN` returns the original unauthenticated handler. A warning is printed, but the default listener accepts connections on every interface. This contradicts the documented required-token boundary for findings. Helm generates a token; direct CLI/container runs do not inherit that protection.

**Change needed:** Require authentication for externally bound API listeners, or require an explicit development flag for unauthenticated operation. Keep local workflows straightforward and document the chosen boundary. Do not silently generate a token that callers cannot obtain.

**Validation needed:** Missing/wrong/correct token, loopback versus external binding, and all API routes. Keep `/healthz` behavior deliberate.

### R26 — OTLP access needs a clear trust boundary

**Evidence:** `engine/internal/ingest/receiver.go:19`; `deploy/helm/argus/templates/networkpolicy.yaml`.

The receiver has no authentication or TLS and runs exports synchronously without admission/concurrency policy. The default gRPC message limit is not a total process-memory bound. The chart permits all pods in allowed namespaces and the engine namespace to access all engine ports. Any allowed sender can inject arbitrary service identities or poison scores.

**Change needed:** Document whether network isolation or receiver credentials establish sender trust. Restrict HTTP and OTLP ingress separately by caller/port, and add appropriate receiver resource limits. The [Kubernetes NetworkPolicy documentation](https://kubernetes.io/docs/concepts/services-networking/network-policies/) explains the peer/port selectors and the requirement for an enforcing network plugin; the project's kindnet environment cannot validate enforcement just by creating the object.

**Validation needed:** Rendered peer/port rules, enforcement on a supporting CNI, unauthorized senders, oversized messages, and concurrent exports. Avoid claiming mTLS or application-level identity checks that do not exist.

### R27 — HTTPS self-telemetry configuration is downgraded to plaintext

**Evidence:** `engine/cmd/argus/serve.go:263`; `engine/internal/telemetry/telemetry.go`, exporter construction.

`otlpEndpointFromEnv` removes either scheme, and `Setup` always sets `WithInsecure`. An `https://` endpoint therefore requests plaintext instead of preserving TLS intent. Exporter construction failures after the trace provider is created also leave that provider unclosed.

**Change needed:** Parse endpoint transport settings explicitly and preserve HTTPS/TLS configuration. Close already-created exporters/providers if later initialization fails.

**Validation needed:** HTTP and HTTPS endpoints, certificate failures, and partial setup failure without leftover goroutines/connections.

### R28 — Redirects can forward custom API keys or tenant headers

**Evidence:** `engine/internal/bench/agent/anthropic.go`, HTTP client and `x-api-key`; `engine/internal/mcp/backend/http.go`, `X-Scope-OrgID`; other authenticated adapters and `plugin/pkg/plugin/app.go`.

Clients follow redirects with the default policy. Go protects certain standard headers for unrelated domains, but custom credentials such as `x-api-key` and `X-Scope-OrgID` do not receive that special handling. The [Go HTTP client documentation](https://pkg.go.dev/net/http#Client) establishes this behavior. An endpoint redirect can therefore send custom secrets to an unintended origin; same-host redirects and HTTPS-to-HTTP changes also deserve a policy.

**Change needed:** Reject redirects for API endpoints unless a known redirect is necessary, or enforce an explicit same-origin/no-downgrade policy. Reject engine URLs containing userinfo, query, or fragment components: simple string concatenation currently accepts configurations that misroute `/api/...` requests. Continue supporting private cluster addresses, which are an intended deployment feature.

**Validation needed:** Redirect to another origin/port, transport downgrade, sensitive header handling, and malformed-but-parseable configured base URLs.

### R29 — Several response/output paths remain unbounded

**Evidence:** JSON decoding in `ingest/mimir/client.go`, `ingest/loki/client.go`, `remediate/llm/openai.go`, `bench/agent/openai.go`, `bench/agent/anthropic.go`, and `bench/judge/judge.go`; subprocess buffers in `bench/agent/shell.go`, `bench/orchestrator/inject.go`, `bench/inject/kube/kube.go`, and `mcp/backend/kube.go`.

Successful HTTP responses are decoded without byte limits, and subprocess stderr/stdout grows in `bytes.Buffer` until completion. Truncating the error after the process exits does not bound capture memory. `mcp/backend/http.go:37` has an 8 MiB limit, but silently returns the prefix of an oversized response as successful raw JSON rather than detecting overflow. Agent conversations can accumulate many large tool answers; a token cap checked after a model call does not bound their byte size.

**Change needed:** Apply explicit response/capture/conversation limits, read one byte beyond limits to detect overflow, and return clear errors or disclosed truncation. Make command cancellation close pipes and terminate the relevant process tree where wrappers spawn children. Redact query/error text before persisting or sharing benchmark reports: truncation alone is not redaction, and `writeBenchReport` currently uses mode 0644.

**Validation needed:** Oversized valid JSON, oversized invalid JSON, endlessly noisy subprocesses, large tool responses, sensitive query literals, and cancellation with child processes.

### R30 — MCP validation and cancellation need tighter bounds

**Evidence:** `engine/internal/mcp/transport.go:34`; `server.go`, range and limit arguments; `tool.go:75`.

The transport accepts an unbounded JSON stream, checks cancellation only before a blocking decode, ignores cancellation notifications, and does not validate the JSON-RPC version. Query handlers parse durations but do not consistently reject reversed ranges, non-positive steps, or huge query limits. Several single-object decoders do not check for trailing data. Advertising a schema does not enforce all these constraints at runtime.

**Change needed:** Bound frames and argument sizes; validate semantic ranges before invoking a backend. Make cancellation work for the owned stdio reader and active tool request, or document unsupported cancellation precisely. Check one complete JSON object where strict input is promised. Retain read-only tool enforcement.

**Validation needed:** Blocked reads canceled by the caller, malformed protocol envelopes, trailing JSON, reversed/large ranges, and excessive limits. No expensive backend call should occur for rejected arguments.

### R31 — Valid empty engine responses can crash the frontend

**Evidence:** `engine/internal/rules/collector.go`, `Snapshot`; `engine/internal/cost/trend.go`; `plugin/src/components/Overview/OverviewContent.tsx`, `Scores/ScoresContent.tsx`, and `Spend/SpendContent.tsx`.

An empty snapshot serializes `services` as null. Overview spreads that field and Scores calls `.filter`, despite the TypeScript contract declaring an array. A storage-only comparison can similarly produce `trend.lines: null`, while Spend reads `.length`. Other APIs already initialize slices, so the wire contract is inconsistent.

**Change needed:** Return arrays consistently from the engine, validate or normalize the API boundary, and render a truthful no-data state. Do not show a success message about every service passing when no services were observed.

**Validation needed:** Responses from the real Go serializers for empty fleets, storage-only reports, empty trends, and absent optional sections, consumed by frontend tests.

### R32 — Service graph sizing does not run when its container mounts

**Evidence:** `plugin/src/components/ServiceGraph/ServiceGraphContent.tsx:64`.

`useContainerWidth` runs its layout effect during the initial loading state, before the ref is attached to a DOM element. The empty dependency list prevents measurement when data mounts the graph. It remains at width 800 until a window resize, and sidebar/layout changes need not emit that event.

**Change needed:** Observe the actual mounted container with a callback ref or `ResizeObserver`, and clean up the observer. Keep the fallback only for an unmeasurable container.

**Validation needed:** Delayed data in a narrow container, sidebar resize without window resize, and observer cleanup after unmount.

### R33 — Pages lack refresh and recoverable request state

**Evidence:** All data pages' initial `useEffect`; `plugin/src/components/Bench/BenchContent.tsx:47`; `plugin/src/components/AppConfig/AppConfig.tsx`, `updatePluginAndReload`.

Most pages fetch once and can remain stale throughout a session despite presenting live state. A failed comparison sets the Bench page's global error, hides its controls, and is not cleared by a successful retry. The comparison display correctly checks the selected pair, but requests still need stale-response/error handling. Settings save failures only reach `console.error`; users see no error or saving state. Empty engine URLs are rejected by the UI even though the backend supports its default URL.

**Change needed:** Add deliberate refresh behavior and last-updated information, use cancelable/request-scoped state, and keep comparison failures separate from the loaded leaderboard. Show settings errors inline and prevent duplicate submissions. Decide consistently how the default engine URL can be restored.

**Validation needed:** Slow/stale requests, switching comparison pairs, failed then successful retries, refresh after an engine change, settings failure, and unmount during fetch.

### R34 — Numeric validation and percentile calculation have boundary gaps

**Evidence:** `engine/internal/stats/stats.go`, `Percentile`; `engine/internal/cost/pricing.go:89`; `engine/internal/synthhist/spec.go:81`; `engine/internal/bench/scenario.go`, numeric checks.

`Percentile` rounds `p*n/100` instead of taking its ceiling despite promising nearest-rank percentiles: P95 of the values 1 through 11 returns 10 rather than 11. This can understate calibration thresholds. Comparison-only validation accepts NaN, because ordinary `<`/`>` checks are false for NaN. Synth specs also accept duplicate service names and do not fully validate rates, jitter, incident error ratios, or duplicate incident identities; invalid inputs can generate decreasing counters or misleading history.

**Change needed:** Implement the documented percentile definition, explicitly reject non-finite values, and validate all synthetic input ranges/identities. Define whether a missing step gets a default while an explicitly invalid step is rejected.

**Validation needed:** Percentiles whose ranks have fractions below/above 0.5, empty/singleton inputs, NaN/infinity YAML, duplicate identities, negative rates, excessive jitter, and invalid incident ratios.

### R35 — Matrix flags and cluster selection can diverge

**Evidence:** `scripts/bench-matrix.sh`, agent-file parsing and port-forward commands.

Agent flags are passed through `xargs`, then later expanded with shell word splitting. Quoted multi-word arguments are no longer preserved. Port-forwards use the current kubectl context, while benchmark injection/tooling can use `ARGUS_KUBE_CONTEXT`. Consequently, telemetry can come from one cluster while the scenario runs on another. Checking only the Mimir port-forward does not establish that Loki/Tempo forwards started successfully.

**Change needed:** Use an explicit argument-array format or a documented parser that preserves quoted arguments without `eval`. Apply the selected cluster consistently to forwards, condition changes, injection, and tools. Verify every required forward before starting a cell, and reject duplicate agent labels and invalid budget values early.

**Validation needed:** Arguments containing spaces/quotes, duplicate labels, different current and selected kube contexts, one failed backend forward, and invalid numeric flags. Use fakes; these checks should not inject faults during ordinary unit testing.

### R36 — Benchmark provenance omits inputs that change behavior

**Evidence:** `engine/internal/bench/orchestrator/orchestrator.go:434`, `HashScenario` and `Report`; `engine/cmd/argus/bench.go`, tool/agent construction; `scripts/bench-matrix.sh`, resume logic.

The scenario hash covers the parsed scenario but not referenced script/manifest contents. Reports do not fingerprint the actual tool surface, injector mode, shell command/arguments, judge configuration, or harness revision. The recorded seed is not passed to the model adapter. The matrix uses a date string as an environment digest and skips any non-empty existing report without validating that its parameters match the requested cell. Existing disclosures about condition labels and answered-run means are useful and should remain.

**Change needed:** Record the behavior-changing inputs and hashes, distinguish metadata from enforced determinism, and validate resumed cells against the requested run configuration. Record a real environment identity where reproducibility is claimed; disclose absent identity instead of treating a date as a digest.

**Validation needed:** Change a referenced script with unchanged YAML, vary tool availability/model/judge settings, switch injector modes, and resume with changed budgets/repeats or a malformed report.

### R37 — Artifact writes can leave partial reports or overwrite another service's patches

**Evidence:** `engine/cmd/argus/bench.go`, `writeBenchReport`; `engine/cmd/argus/remediate.go`, `fileSafe`/`writePatches`; `engine/cmd/argus/serve_bench.go`, report directory loading.

Direct writes can expose a partially written JSON report to the leaderboard or leave a non-empty truncated file that matrix resume treats as complete. Sanitization maps different names such as `a/b` and `a_b` to the same patch filename, so one service can overwrite another's remediation. Output paths already received path-traversal hardening; that does not solve collisions or atomicity.

**Change needed:** Stage artifacts beside the destination and atomically replace them after successful rendering. Detect collisions or include a stable service-identity suffix. Define overwrite behavior explicitly, and validate a report before marking a cell complete.

**Validation needed:** Interrupted/failed writes, concurrent leaderboard reads, existing outputs, and colliding service names. Preserve existing path confinement.

### R38 — CI misses important existing tests and checks

**Evidence:** `.github/workflows/ci.yml:58`; `Makefile`; `plugin/package.json`; `plugin/tests`; `engine/internal/store/postgres_integration_test.go`.

The plugin CI job builds the Go backend but never runs its tests. `make test` also omits plugin Go tests. There is no npm audit step, frontend coverage gate, Playwright job, or Postgres integration job. `test:ci` permits zero tests. A formatter check is absent even though six frontend files currently differ from Prettier. Backend security checks do not exercise authentication/proxy behavior tests.

**Change needed:** Run existing plugin backend tests in CI and the documented local test target. Add focused integration and browser smoke jobs with required services. Make an accidental disappearance of tests fail. Add dependency/format checks and meaningful branch/behavior coverage requirements without allowing unavailable integration services to look like a passing job.

**Validation needed:** Verify workflow execution on a PR with a deliberately failing backend test, absent test files, a broken API-to-UI contract, and a failing store migration. Keep the existing engine race, depguard, dogfood, and core coverage checks.

## Readability and cleanup recommendations

These are smaller changes to make alongside focused fixes. They do not justify replacing the architecture or introducing a broad shared framework.

| ID | Location | Observation and recommended cleanup |
| --- | --- | --- |
| C01 | `plugin/src/components/{Overview,Scores,ServiceGraph,Spend}` and `plugin/src/plugin.json` | Run the existing Prettier configuration on the six failing files: OverviewContent.tsx, ScoresContent.tsx, ServiceGraphContent.tsx, SpendContent.tsx, SpendContent.test.tsx, and plugin.json. Expand crowded JSX table cells/rows so each field is easy to inspect. |
| C02 | `plugin/src/components/*`, page comments | Replace repeated product-positioning and architecture-rule paragraphs with short explanations of actual state/lifecycle behavior. Preserve visible confidence, pricing, and replay caveats; those are product requirements. |
| C03 | `engine/internal/bench/agent/agent.go`, `loop.go` | Keep the shared provider loop, which avoids protocol duplication. Shorten comments recounting individual flagship runs and move experiment history to docs. Retain explanations of final-turn budgeting, submission correction limits, and token accounting beside the relevant transition. |
| C04 | `engine/internal/backtest/rules.go`, deployment files, `plugin/src/README.md`, LLM package comments | Remove stale phase/scaffold claims and fill in plugin publishing boilerplate. Examples: backtest is no longer just a spike; Compose/Helm still say Phase 0/1; the LLM comment claims an Anthropic implementation in that package when the native adapter lives in bench agents; the OpenAI bench constructor comment says 60 seconds while its default is ten minutes. |
| C05 | `engine/internal/rules/loader.go`, `LoadBytes` | Replace `err.Error() == "EOF"` with `errors.Is(err, io.EOF)`. Do not use error message text as control flow. Keep strict YAML fields and the `os.Root` confinement. |
| C06 | `engine/internal/remediate/remediate.go`, `Render`/`Available` | Compute the available template registry once and parse embedded templates once. The current render repeatedly scans the embedded directory and reparses both formats. Use a small package-local registry, not a new templating abstraction. |
| C07 | `engine/internal/remediate/remediate.go:88` | Use the existing numeric conversion helper for cardinality. Current code accepts only `float64`, while live aggregate rows use `int64`; JSON-loaded CLI findings and HTTP-rendered findings therefore produce different comments. |
| C08 | `engine/internal/cost/cost.go`, `trend.go` | Accumulate totals in a fixed order and sort on every grouping field. Iterating maps while summing floats or sorting on an incomplete key undermines the comments promising deterministic output. |
| C09 | HTTP adapters and report/error helpers | Consolidate repeated request policy only where the semantics really match: context, byte caps, redirect policy, error redaction, and response validation. Keep provider-specific wire types close to their adapters. Similar-looking truncation helpers currently have different meanings; name/document those differences before attempting to merge them. |
| C10 | `plugin/src/api/types.ts`, Go JSON contracts | Stop declaring arrays that the server may encode as null. Prefer consistent Go serialization and a small boundary validator over repeated non-null assertions such as `data.lifecycle!`. Add typed responses where handlers currently build a stable public API from `map[string]any`. |
| C11 | `plugin/src/components/AppConfig/AppConfig.tsx` | Keep the settings form's asynchronous state close to the form; remove the disconnected fire-and-forget helper that can only log errors. Clear names for saving, saved, and failed states improve both readability and behavior. |
| C12 | `plugin/playwright.config.ts` | Remove commented-out dotenv code and generic scaffold tutorials that add no project-specific guidance. Keep only explanations of authentication setup, retries, traces, and supported browser/environment choices. |
| C13 | `.github/workflows/release.yml:52`, `:61`; Docker/Compose image tags | Use reviewed/pinned release tools instead of `latest`; the sign-plugin package is already a lockfile dependency and can be invoked through the existing script. Consider immutable action/image references for reproducible releases. Preserve the draft-release behavior. |
| C14 | `engine/.golangci.yml` | Revisit global security exclusions after bounding/redacting artifacts. The justification that output never contains secrets is too broad for findings, query logs, and explanations. Keep necessary CLI file-path allowances narrow and explained; do not weaken checks to hide failures. |
| C15 | `engine/internal/stats/stats.go`, `Max`; `engine/internal/calibrate` | Calculate maxima with a single scan instead of sorting a copied slice. Where calibration repeatedly computes statistics for the same distribution, sort once and reuse the result while keeping public helpers non-mutating. Do this after correcting percentile semantics. |
| C16 | `plugin/pkg/plugin/app.go:73`; backend client creation | Give long-lived clients explicit ownership. Close idle connections on disposal when using an instance-owned transport; do not indiscriminately close the global shared default transport. Reuse clients during repeated backtest recomputations where configuration is unchanged. |

Do not rename normal Go receivers merely because they are short. Names such as `ctx`, `err`, and small receiver identifiers are idiomatic. Spend naming effort on domain concepts, ambiguous state, and public contracts. Comments should explain a constraint or reason; avoid narrating each statement or repeatedly advertising that code is deterministic, bounded, or read-only when those claims need tests.

## Test additions needed

The temporary probes demonstrated missing regression coverage; they were not committed. Add permanent behavior tests with each corresponding fix.

1. **Collector and ingestion:** bounded service admission; observation-window expiry; bad-to-good recovery; evaluation errors; long-value hashing; evidence retention limits; trace overflow and finite ratios; deterministic capped graphs; CLI trace-window finalization; and empty-fleet score gates.
2. **Backend contracts:** fixtures taken from supported Mimir responses; top-value truncation; active counting; per-service cardinality scope; combining multiple pair outcomes; malformed/missing fields; oversized response handling; and redirect/header policies.
3. **Backtest:** rejected non-positive steps; cyclic/bare/nested synthesis; unsupported temporal/label semantics; identical segment requirements for diff; insufficient coverage; per-rule missing inputs; static alert labels; and continued-firing behavior.
4. **Cost:** a true week-old baseline; absent/incompatible baseline; complete attribution; verified finding quantities; empty/storage-only wire contracts; deterministic team ordering; and rejection of non-finite rates/usage.
5. **Benchmark:** cleanup errors preserved alongside diagnoses; no contaminated follow-up repeat; independent runner ownership; complete provenance; atomic output/resume validation; shell argument preservation; bounded process output; and process-tree cancellation.
6. **Plugin:** real empty Go responses; delayed graph mounting/resizing; comparison failure/retry; canceled/stale requests; settings save failures; refresh behavior; and authenticated/unauthenticated proxy requests. Add a Grafana browser smoke test for the actual backend resource routes.
7. **Privacy and remediation:** assert the entire outbound LLM payload contains no sensitive markers by default; parse generated YAML/Alloy; test unusual quoting/delimiters; and verify service-scoped transformation behavior and filename collision handling.
8. **Deployment and CI:** rendered listeners/ports/token wiring; real demo health and telemetry; backend tests running in CI; Postgres migration/rollback/round-trip integration; and test/coverage jobs failing when required data or services are absent.

Coverage policy should target the behaviors above. Retain the existing 70% core gate. Add a reasonable frontend branch floor after these tests, based on an explicitly defined source inclusion set. Avoid tests that merely duplicate implementation expressions, snapshots of model prose, or blanket exclusions that inflate percentages. For integration-only storage, report integration coverage separately instead of describing the unit run's 0% as proof that no tests exist.

## Existing choices worth preserving

- Strict rule parsing and directory confinement with `os.Root` are valuable safeguards.
- The deterministic scorer and LLM edges are separated, and depguard protects key package boundaries.
- SQL calls use parameters, and score snapshot writes use a transaction.
- Backend calls generally propagate contexts and have HTTP timeouts.
- API authentication uses constant-time comparison when configured; the plugin stores the token in Grafana's secure settings.
- The React views render evidence as text rather than raw HTML.
- Reports disclose incomplete spec implementation, sampled confidence, replay limitations, normalization method, and answered-run means. These disclosures should survive cleanup.
- Provider wire formats share a common agent loop; a wholesale rewrite would add risk without resolving the specific defects above.

## Suggested implementation order

Address the dependency/security boundary, collector limits/evaluation correctness, Mimir contracts, redaction, and generated-patch safety first. Then fix benchmark restoration/ownership and the shipped deployment wiring. Correct backtest/cost semantics and API/UI empty-state behavior next, with regression tests in the same changes. Finish with small formatting/comment cleanups and CI additions that enforce the new behavior.

Keep changes reviewable by problem area. No source edits, new permanent tests, dependency upgrades, infrastructure actions, or formatting fixes were made as part of this review.
