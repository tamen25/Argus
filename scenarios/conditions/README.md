# Telemetry conditions (the flagship experiment)

The flagship benchmark (master plan §3.2) runs the scenario suite twice: once
against degraded telemetry, once after applying Argus's remediations, and
reports the change in agent accuracy. These files put the dev cluster's
telemetry into each condition.

```bash
scenarios/conditions/apply.sh degraded      # from WSL, where make dev-up runs
argus bench run --condition degraded ...    # label every run to match
scenarios/conditions/apply.sh remediated
argus bench run --condition remediated ...
scenarios/conditions/apply.sh baseline      # put it back
argus bench report --compare degraded,remediated runs/
```

| Condition | What changes | Argus rules that see it |
|---|---|---|
| `baseline` | nothing: the pipeline from `deploy/kind/values/alloy.yaml` | — |
| `degraded` | server spans lose their parent (except checkout and payment); log records lose their trace and span ids | SPA-002, SPA-004, ARG-SPA-002, ARG-LOG-001 |
| `remediated` | Argus's `missing-resource-attributes` patch: Kubernetes identity, `service.version`, `deployment.environment.name` | ARG-RES-002, ARG-RES-003 (fixed) |

Each condition is a stage in the Alloy pipeline, between the OTLP receiver and
the batch processor, so nothing in the demo application is redeployed.

**`apply.sh` proves the condition before returning.** After the `helm upgrade`
it waits until the condition shows in the telemetry, the way a scenario's
steady-state gate works, and fails if it never does:

- degraded: no service-to-service edges in the service graph except the two
  the gates need;
- remediated: edges present and workloads reporting `k8s.deployment.name`;
- baseline: edges present and no workload reporting it.

The applied condition is recorded in the ConfigMap `lgtm/argus-bench-condition`.

## Rules for these files

- **`remediated.alloy` is Argus's own output.** It is the
  `missing-resource-attributes` template exactly as `argus remediate` renders
  it, with its one placeholder filled in. A test fails if it drifts; regenerate
  it when the template changes.
- **`degraded.alloy` is our construction**, and results built on it must say so.
  Every degradation in it is one Argus's rules detect.
- **Neither may break a scenario gate.** The gates read app metrics,
  `target_info` and the checkout → payment service-graph edge, so the degraded
  condition touches traces and logs only and leaves checkout's and payment's
  server spans alone.
