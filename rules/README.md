# Built-in rules

Rule definitions (YAML + CEL) load from here in Phase 1. Two trees, kept
strictly separate (CLAUDE.md):

- `spec/` — rules implementing the upstream
  [Instrumentation Score specification](https://github.com/instrumentation-score/spec).
  Spec version is pinned in `.instrumentation-score-version` (Phase 1).
- `argus/` — Argus extension rules beyond the spec (cost-aware rules, LGTM
  specifics). Candidates here get contributed upstream when they generalize.

Rule schema is versioned; the loader rejects unknown fields. Adding a
common-case rule requires zero Go changes (architecture rule 4).
