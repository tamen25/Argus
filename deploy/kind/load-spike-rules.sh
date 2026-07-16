#!/usr/bin/env bash
# Load the fidelity-spike rule group into the dev Mimir ruler (kind cluster).
# Idempotent: POSTing the same group name replaces it.
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

kubectl -n lgtm delete pod argus-ruleload --ignore-not-found >/dev/null 2>&1 || true
kubectl -n lgtm run argus-ruleload --restart=Never --image=curlimages/curl \
  --overrides='{"spec":{"containers":[{"name":"argus-ruleload","image":"curlimages/curl","command":["sleep","120"]}]}}' >/dev/null
kubectl -n lgtm wait --for=condition=ready pod/argus-ruleload --timeout=60s >/dev/null

kubectl -n lgtm cp "$dir/mimir-rules/argus-spike.yaml" argus-ruleload:/tmp/argus-spike.yaml
kubectl -n lgtm exec argus-ruleload -- curl -sf -X POST \
  -H 'X-Scope-OrgID: anonymous' \
  -H 'Content-Type: application/yaml' \
  --data-binary @/tmp/argus-spike.yaml \
  http://mimir-gateway.lgtm.svc/prometheus/config/v1/rules/argus-spike

echo "loaded; ruler state:"
kubectl -n lgtm exec argus-ruleload -- curl -sf \
  -H 'X-Scope-OrgID: anonymous' \
  http://mimir-gateway.lgtm.svc/prometheus/api/v1/rules | head -c 400
echo
kubectl -n lgtm delete pod argus-ruleload >/dev/null
