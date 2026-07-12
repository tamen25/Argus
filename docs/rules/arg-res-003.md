# ARG-RES-003 — deployment.environment.name is present

**Impact:** Normal (weight 20) · **Target:** resource · **Source:** argus
extension

Without `deployment.environment.name`, prod and staging telemetry blend —
and [LOG-001](log-001.md) cannot even identify production.

**Remediation.** Template `missing-resource-attributes`.
