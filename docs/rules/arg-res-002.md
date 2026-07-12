# ARG-RES-002 — service.version is present

**Impact:** Important (weight 30) · **Target:** resource · **Source:** argus
extension (the spec's illustrative examples rate this Important, but no
official rule file exists yet — upstream contribution candidate)

Without `service.version`, regressions cannot be correlated to deploys.

**Remediation.** Template `missing-resource-attributes`: set
`OTEL_RESOURCE_ATTRIBUTES=service.version=$VERSION` at build/deploy time, or
stamp it in the collector.
