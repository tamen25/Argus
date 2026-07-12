#!/usr/bin/env bash
# Toggle an otel-demo feature flag by patching the flagd ConfigMap — the fault
# injection mechanism Phase 4 bench scenarios build on (flagd-ui is removed).
# NOTE: the otel-demo chart copies the ConfigMap into an emptyDir via an
# initContainer, so flagd only sees changes after a pod restart — this script
# does the rollout restart for you.
#
# Usage: toggle-flag.sh <flagKey> <variant> [namespace]
#   e.g. toggle-flag.sh adFailure on
#        toggle-flag.sh adFailure off
#
# Prints the OFREP-observed value once flagd picks the change up.
# REMINDER: log induced faults in incidents.yaml (master plan §9).
set -euo pipefail

FLAG="${1:?flag key required}"
VARIANT="${2:?variant required (e.g. on|off)}"
NS="${3:-otel-demo}"
CM="flagd-config"
FILE="demo.flagd.json"

command -v jq >/dev/null || { echo "ERROR: jq required" >&2; exit 1; }

current=$(kubectl -n "$NS" get cm "$CM" -o jsonpath="{.data['demo\.flagd\.json']}")
echo "$current" | jq -e --arg f "$FLAG" '.flags[$f]' >/dev/null \
  || { echo "ERROR: flag '$FLAG' not found. Available:" >&2; echo "$current" | jq -r '.flags | keys[]' >&2; exit 1; }
echo "$current" | jq -e --arg f "$FLAG" --arg v "$VARIANT" '.flags[$f].variants | has($v)' >/dev/null \
  || { echo "ERROR: variant '$VARIANT' not defined for '$FLAG'" >&2; exit 1; }

updated=$(echo "$current" | jq --arg f "$FLAG" --arg v "$VARIANT" '.flags[$f].defaultVariant = $v')
kubectl -n "$NS" create cm "$CM" --from-literal="$FILE=$updated" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "patched: $FLAG -> $VARIANT ($(date -u +%FT%TZ)); restarting flagd to pick it up..."
kubectl -n "$NS" rollout restart deploy/flagd >/dev/null
kubectl -n "$NS" rollout status deploy/flagd --timeout=3m >/dev/null

expected=$(echo "$updated" | jq -c --arg f "$FLAG" '.flags[$f].variants[.flags[$f].defaultVariant]')
for i in $(seq 1 30); do
  got=$(kubectl -n "$NS" run "ofrep-check-$$" --rm -i --restart=Never --quiet \
        --image=curlimages/curl:8.10.1 -- \
        -s -X POST "http://flagd:8016/ofrep/v1/evaluate/flags/${FLAG}" \
        -H 'Content-Type: application/json' -d '{"context":{}}' 2>/dev/null | jq -c '.value' 2>/dev/null) || got=""
  if [ "$got" = "$expected" ]; then
    echo "flagd serving $FLAG=$got ($(date -u +%FT%TZ)) — OK"
    exit 0
  fi
  sleep 10
done
echo "ERROR: flagd did not serve expected value within 5m (last: ${got:-none}, want: $expected)" >&2
exit 1
