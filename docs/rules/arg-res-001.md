# ARG-RES-001 — service.name is not the SDK default

**Impact:** Critical (weight 40) · **Target:** resource · **Source:** argus
extension — findings and extension score only, **never** part of the spec
score (the spec forbids blending non-spec rules).

`service.name` MUST NOT be an SDK-generated default (`unknown_service` or
`unknown_service:<process>`). Such names satisfy RES-005's letter (non-empty)
but mean the SDK was never configured: telemetry is technically attributed,
practically anonymous.

**Detection.** Sampled stream only: fails any resource whose `service.name`
starts with `unknown_service`.

**Remediation.** Same as [RES-005](res-005.md): configure `OTEL_SERVICE_NAME`
or set it via collector/Alloy resource processors.

This rule is a candidate for upstream contribution to the Instrumentation
Score spec (success criterion 5 in the master plan).
