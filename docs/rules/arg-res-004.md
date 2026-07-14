# ARG-RES-004 — resource attributes are consistent per service

**Impact:** Normal (weight 20) · **Target:** resource · **Source:** argus
extension

Master-plan rule 15: one service reporting many conflicting values for
identity attributes usually means broken resource detection or two pipelines
writing as one service.

**Detection.** HLL sketch per (service, attribute) over the values of
`service.version`, `deployment.environment.name`, `telemetry.sdk.language`
(`resource_attr_cardinality` aggregate). Fails above `params.max_values`
(default **3** — rolling deploys legitimately run 2-3 versions
simultaneously; flagged for tuning).

**Remediation.** Template `missing-resource-attributes`.
