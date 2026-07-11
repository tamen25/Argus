# EKS demo environment (Phase 4 only)

Thin Terraform layer for the flagship bench runs. Nothing is built here until
Phase 4. Binding rules (master plan §4, CLAUDE.md):

- **apply/destroy per session** — never leave EKS running
- EKS is reserved for the headline scenario subset only (~2 scenarios × all
  agents × both telemetry conditions); the full matrix runs on kind
- Before the first full Phase 4 run: compute projected cost (wall-clock, API
  tokens, EKS hours) and confirm with the user
