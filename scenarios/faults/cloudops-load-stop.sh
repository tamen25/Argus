#!/usr/bin/env bash
# Cleanup hook for the CloudOps mesh scenarios: stop the load cloudops-load.sh
# started. Reset's label sweep would also remove it, but only before the next
# run; without this the load would run on to its deadline after the scenario.
# Idempotent: with no load running it changes nothing.
set -euo pipefail

ns="${ARGUS_NAMESPACE:-cloudops}"
[ -n "$ns" ] || ns=cloudops
ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then ctx=(--context "$ARGUS_KUBE_CONTEXT"); fi

kubectl "${ctx[@]}" -n "$ns" delete job -l argus.dev/fault=cloudops-load --ignore-not-found >/dev/null
echo "load stopped"
