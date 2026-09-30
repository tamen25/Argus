# Bench scenarios (Module C — Phase 4)

One YAML file per scenario (`apiVersion: argus/v1alpha1`, `kind: BenchScenario`,
schema in master plan §3.2). Fault manifests and scripts live in `faults/`.
`categories.yaml` is the closed list of fault categories every agent is offered;
a scenario's category must be on it.

**Before adding one, read [docs/bench/authoring-scenarios.md](../docs/bench/authoring-scenarios.md).**
Every rule in it is a failure that already happened.

If you induce a fault by hand (testing Chaos Mesh, generating history), label it
`app.kubernetes.io/managed-by: argus-bench` so the next run's reset sweeps it up,
commit the manifest to `faults/`, and log the incident in `/incidents.yaml`.

## Shipped

| Scenario | Fault | Ground truth | Category | Gate |
|---|---|---|---|---|
| `cardinality-explosion-frontend` | unbounded path parameter floods a metric label | `frontend` | `cardinality-explosion` | live series |
| `redis-latency-cart` | 4s network delay on the cart's datastore | `valkey-cart` | `dependency-latency` | p95 latency |
| `network-partition-product-catalog` | a healthy service cut off from all callers | `product-catalog` | `network-partition` | p95 latency |
| `egress-blackhole-frontend` | one caller cut off from three healthy backends | `frontend` | `network-partition` | 5xx rate |
| `oomkill-checkout` | checkout's memory limit set below its working set | `checkout` | `oomkill` | absence |
| `missing-service-name-payment` | payment redeployed with an empty `OTEL_SERVICE_NAME` | `payment` | `missing-service-name` | `unknown_service` series |
| `broken-trace-propagation-payment` | payment redeployed with `OTEL_PROPAGATORS=none` | `payment` | `broken-trace-propagation` | service-graph edge share |
| `deploy-regression-cart` | cart redeployed with its cache address on a closed port | `cart` | `deploy-regression` | 5xx rate |

Floor is 8, target 12 (master plan §6.5).

Each is built so the obvious answer is wrong: the loudest service is a decoy and
the root cause sits a hop or more from the symptom. `egress-blackhole-frontend`
and `network-partition-product-catalog` are deliberate near-opposites — a broken
caller reporting healthy services as unreachable, and a healthy service every
caller reports as down. `deploy-regression-cart` mirrors `redis-latency-cart`:
the same errors naming the cache, but here the cache is healthy and the fault is
cart's own rollout. The two payment scenarios break telemetry, not the service:
nothing errors, and the evidence is traffic that is still there under another
name, or no longer linked.

The last three mutate an existing workload through `faults/lib/env-fault.sh` and
reset and clean up with `faults/restore-all-mutations.sh`, which repairs every
recorded mutation in the namespace (see the authoring guide, rule 6).

## Validation

The first five were run through the full lifecycle on the kind cluster (2026-09-30):
reset, verified-clean baseline, inject, steady state, answer, cleanup — each
scoring 1.00 with the `cited` stub and leaving the cluster clean. Scenario 1 was
also run with `--repeats 2`; the second repeat re-established its own fault
rather than passing on the first one's residue. Scenario 1's full write-up:
[docs/bench/scenario-01-validation.md](../docs/bench/scenario-01-validation.md).

The three mutation scenarios took the same path the same day, each at 1.00:
`missing-service-name-payment` in 1m44s, `broken-trace-propagation-payment` in
3m04s, `deploy-regression-cart` in 1m49s, wall clock from reset to cleanup. Each
left its workload byte-identical to before: payment's `OTEL_SERVICE_NAME` back
on its downward-API `fieldRef`, `OTEL_PROPAGATORS` removed (it was never set),
cart's `VALKEY_ADDR` back on 6379. Two mutations leaked together (payment and
cart) were both repaired by one run of `restore-all-mutations.sh`.

## Calibration

Every shipped scenario gives this, and a test enforces the rubric floor that
makes it possible:

| `--stub-profile` | Score |
|---|---|
| `vague` | no diagnosis |
| `obvious` | 0.00 |
| `shotgun` (with the scenario's decoys) | 0.00 |
| `cited` | 1.00 |

## Still to author

The floor of 8 is met. Toward the target of 12: disk pressure → Loki ingestion
drop, alert storm, certificate expiry, S3 backend throttling.
