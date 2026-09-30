#!/usr/bin/env bash
# Reset/cleanup hook for cardinality-explosion-frontend.
#
# Deleting the load driver stops NEW series, but not the old ones: frontend's
# OTel SDK exports cumulative metrics and keeps every attribute set it has seen
# in memory, so it goes on reporting the fault's series after the fault is gone.
# Restarting frontend is what actually removes the fault's effect. Without it a
# second repeat passes its steady-state gate on the first repeat's residue, and
# the next scenario's agent inherits an inflated frontend as a confounder.
#
# Run by `argus bench run --inject=auto`, which sets ARGUS_NAMESPACE and
# ARGUS_KUBE_CONTEXT so this targets the same cluster as the manifest steps.
set -euo pipefail

ns="${ARGUS_NAMESPACE:-otel-demo}"
[ -n "$ns" ] || ns=otel-demo
ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then
  ctx=(--context "$ARGUS_KUBE_CONTEXT")
fi

kubectl "${ctx[@]}" -n "$ns" rollout restart deployment/frontend
kubectl "${ctx[@]}" -n "$ns" rollout status deployment/frontend --timeout=5m
