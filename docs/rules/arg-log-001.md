# ARG-LOG-001 — logs carry trace correlation

**Impact:** Important (weight 30) · **Target:** log · **Source:** argus
extension (extension score only)

Master-plan rule 4: uncorrelated logs cannot be joined to traces during
incidents.

**Detection.** Violation per record without a trace_id; the service fails
only when **more than 50%** of its log volume is uncorrelated
(`threshold_ratio: 0.5`) — background jobs and startup logs legitimately lack
trace context. Threshold flagged for real-world tuning.

**Remediation.** Template `logs-without-trace-context`: use the OTel log
bridge/appender so active span context stamps records.
