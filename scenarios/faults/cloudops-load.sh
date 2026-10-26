#!/usr/bin/env bash
# Steady order traffic for the CloudOps mesh scenarios (cloudops-*.yaml).
#
# CloudOps's own load is a trickle: one minute in every five. A [2m] gate over
# that flickers on and off, and an agent would investigate a cluster that is
# idle most of the time. This starts one continuous run instead: a Job cloned
# from the loadgen-trickle CronJob (same image, gateway address and OTel
# settings), placing orders at 5 rps for 20 minutes.
#
# The clone keeps the trickle's service name, so the traffic looks like the
# cluster's normal load rather than a new service appearing at fault time.
# It carries the sweep label, so reset removes a leftover one, and the
# scenarios stop it in cleanup (cloudops-load-stop.sh). Its pods carry the
# label too, which hides them from the agent's topology.
#
# Needs the CronJob (CloudOps enable_loadgen and loadgen_enable_trickle, both
# on with the mesh) and jq. Run by `argus bench run --inject=auto`
# (ARGUS_NAMESPACE, ARGUS_KUBE_CONTEXT).
set -euo pipefail

ns="${ARGUS_NAMESPACE:-cloudops}"
[ -n "$ns" ] || ns=cloudops
ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then ctx=(--context "$ARGUS_KUBE_CONTEXT"); fi
k() { kubectl "${ctx[@]}" -n "$ns" "$@"; }

JOB=argus-bench-load
RPS=5
DURATION=1200

if [ -z "$(k get cronjob loadgen-trickle --ignore-not-found -o name)" ]; then
  echo "no loadgen-trickle CronJob in ${ns}: CloudOps needs enable_loadgen and loadgen_enable_trickle" >&2
  exit 1
fi

k delete job "$JOB" --ignore-not-found --wait=true >/dev/null
k create job "$JOB" --from=cronjob/loadgen-trickle --dry-run=client -o json \
  | jq --arg rps "$RPS" --arg duration "$DURATION" --argjson deadline $((DURATION + 60)) '
      .metadata.labels["app.kubernetes.io/managed-by"] = "argus-bench"
      | .metadata.labels["argus.dev/fault"] = "cloudops-load"
      | .spec.template.metadata.labels["app.kubernetes.io/managed-by"] = "argus-bench"
      | .spec.activeDeadlineSeconds = $deadline
      | .spec.template.spec.containers[0].env |= map(
          if .name == "RUN_ON_START" then .value = "place-order"
          elif .name == "RUN_RPS" then .value = $rps
          elif .name == "RUN_DURATION_SECONDS" then .value = $duration
          else . end)' \
  | k apply -f - >/dev/null

# Return once the load is actually running, so the fault that follows meets
# traffic rather than an idle mesh.
k wait --for=condition=Ready pod -l "job-name=${JOB}" --timeout=2m >/dev/null
echo "load running: place-order at ${RPS} rps for ${DURATION}s (job ${JOB})"
