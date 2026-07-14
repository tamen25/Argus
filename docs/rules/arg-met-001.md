# ARG-MET-001 — histograms carry exemplars

**Impact:** Normal (weight 20) · **Target:** metric · **Source:** argus
extension (extension score only)

Master-plan rule 8: exemplars are the bridge from a latency spike to an
example trace.

**Detection.** Coverage-based via the `exemplar_coverage` aggregate
(histogram points seen vs. points carrying exemplars, per service): the
service **passes if any histogram point in the window carried an exemplar** —
exemplars are sparse by design, so per-point ratios would always fail
honest instrumentations. Zero exemplars across a whole window = bridge down.

**Remediation.** Template `missing-exemplars`: enable exemplar collection in
the SDK metric reader and keep trace context active where histograms record.
