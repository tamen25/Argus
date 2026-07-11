# Bench scenarios (Module C — Phase 4)

One YAML file per scenario (`apiVersion: argus/v1alpha1`, `kind: BenchScenario`,
schema in master plan §3.2). Fault manifests (Chaos Mesh, kubectl, scripts) live
in `faults/`.

Nothing here until Phase 4 — but if you induce a fault on the dev cluster
before then (testing Chaos Mesh, generating history), commit the manifest to
`faults/` and log the incident in `/incidents.yaml`.
