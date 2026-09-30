# Bench scenarios (Module C — Phase 4)

One YAML file per scenario (`apiVersion: argus/v1alpha1`, `kind: BenchScenario`,
schema in master plan §3.2). Fault manifests and scripts live in `faults/`.

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

Floor is 8, target 12 (master plan §6.5).

Each is built so the obvious answer is wrong: the loudest service is a decoy and
the root cause sits a hop or more from the symptom. `egress-blackhole-frontend`
and `network-partition-product-catalog` are deliberate near-opposites — a broken
caller reporting healthy services as unreachable, and a healthy service every
caller reports as down.

## Validation

All five were run through the full lifecycle on the kind cluster (2026-09-30):
reset, verified-clean baseline, inject, steady state, answer, cleanup — each
scoring 1.00 with the `cited` stub and leaving the cluster clean. Scenario 1 was
also run with `--repeats 2`; the second repeat re-established its own fault
rather than passing on the first one's residue. Scenario 1's full write-up:
[docs/bench/scenario-01-validation.md](../docs/bench/scenario-01-validation.md).

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

Toward the floor of 8, all mutations of an existing workload and so `script`
steps with restore hooks: broken trace propagation, missing `service.name`,
deploy regression (bad image). Then disk pressure → Loki ingestion drop, alert
storm, certificate expiry, S3 backend throttling.
