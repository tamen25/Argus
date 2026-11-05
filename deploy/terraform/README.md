# EKS runs

Argus has no Terraform of its own. Its EKS runs use
[CloudOps](https://github.com/tamen25/CloudOps), which provisions EKS with an
LGTM stack, a polyglot mesh, Chaos Mesh and the Argus engine (its
`modules/argus` installs this repository's Helm chart), and is the environment
the `scenarios/cloudops-*.yaml` bench scenarios target. Two sessions on
2026-11-05 deployed, scored, benchmarked and destroyed it:
CloudOps `docs/reviews/2026-11-05-eks-run.md`, and here
`docs/bench/cloudops-model-run.md`.

To run Argus on a CloudOps cluster: CloudOps `docs/runbooks/argus.md` (the
engine in the cluster) and `scenarios/README.md`, "CloudOps mesh" (the bench).

The rules for EKS still bind (master plan §3.2, §4, §12):

- apply and destroy within the session; never leave EKS running;
- EKS is for the headline subset; the full matrix runs on kind;
- compute the projected cost (wall-clock, tokens, EKS hours) and get the
  maintainer's approval before any apply (CloudOps keeps the estimate:
  `docs/cost/2026-10-20-argus-run-estimate.md`).
