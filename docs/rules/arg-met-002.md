# ARG-MET-002 — histograms have explicit bucket bounds

**Impact:** Important (weight 30) · **Target:** metric · **Source:** argus
extension

Master-plan rule 12 (presence half; layout consistency is
[MET-004](met-004.md)): a classic histogram without explicit bounds is a
sum/count pair pretending — no quantiles can ever be computed from it.

**Detection.** Per-point: `metric.type == 'histogram'` with empty
`bucket_bounds` is a violation. Zero tolerance.

**Remediation.** Template `histogram-bucket-mismatch`: define explicit
buckets in the SDK view or collector.
