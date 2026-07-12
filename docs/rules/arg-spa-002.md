# ARG-SPA-002 — traces have a root span

**Impact:** Normal (weight 20) · **Target:** span · **Source:** argus
extension (separate extension score, never the spec score)

Master-plan rule 10: rootless traces cannot be anchored to an entry point,
breaking end-to-end latency attribution.

**Detection.** Trace tracker (`trace_health.missing_root_ratio`), completed
windows only. Fails above `params.max_missing_root_ratio` (default **0.2** —
deliberately loose because head sampling drops roots legitimately; same
caveat as [SPA-002](spa-002.md)).

**Remediation.** Template `broken-context-propagation`.
