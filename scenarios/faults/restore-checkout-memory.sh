#!/usr/bin/env bash
# Reset/cleanup hook for oomkill-checkout: put back checkout's original memory
# limit and rollout strategy, recorded as annotations by oomkill-checkout.sh.
#
# Idempotent and safe as a reset: with no recorded originals there is nothing
# to restore and it exits 0. The annotations are removed only AFTER the rollout
# succeeds, so a restore that fails partway can simply be run again.
set -euo pipefail

ns="${ARGUS_NAMESPACE:-otel-demo}"
[ -n "$ns" ] || ns=otel-demo
ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then ctx=(--context "$ARGUS_KUBE_CONTEXT"); fi
k() { kubectl "${ctx[@]}" -n "$ns" "$@"; }

LIMIT_KEY=argus-oomkill-original-limit
STRATEGY_KEY=argus-oomkill-original-strategy

limit="$(k get deploy checkout -o jsonpath="{.metadata.annotations.${LIMIT_KEY}}")"
if [ -z "$limit" ]; then
  echo "checkout: no recorded originals, nothing to restore"
  exit 0
fi
strategy="$(k get deploy checkout -o jsonpath="{.metadata.annotations.${STRATEGY_KEY}}")"

k patch deploy checkout --type=strategic -p \
  "{\"spec\":{\"strategy\":${strategy},\"template\":{\"spec\":{\"containers\":[{\"name\":\"checkout\",\"resources\":{\"limits\":{\"memory\":\"${limit}\"}}}]}}}}" >/dev/null
k rollout status deploy/checkout --timeout=5m
k annotate deploy checkout "${LIMIT_KEY}-" "${STRATEGY_KEY}-" >/dev/null
echo "checkout restored to ${limit}"
