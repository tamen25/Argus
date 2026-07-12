# Argus Instrumentation Score Report

- Generated: 2026-07-12T03:00:00Z · argus v0.1.0-test · window 60s
- Instrumentation Score spec e6ee22274284
- **Fleet score: 50.0** (Needs Improvement)

> ⚠️ This implementation does not yet implement the full rule set of the Instrumentation Score specification (rules evaluated: MET-001, RES-005). Scores may differ from a complete implementation.

> Note: cardinality tracker overflowed 3 pairs

## Services

| Service | Score | Category | Extension | Findings |
|---|---:|---|---:|---:|
| checkout | 100.0 | Excellent | — | 0 |
| unknown_service:java | 0.0 | Poor | 0.0 | 1 |

## Findings

### unknown_service:java — service.name is present (`RES-005`)

- impact: **critical** · source: spec · confidence: **sampled**
- observed: 4 · violations: 4 (100%)
- Resource attributes MUST contain a non-empty service.name.
  - evidence (span): span unnamed
