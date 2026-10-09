#!/usr/bin/env bash
# Put the dev cluster's telemetry into a bench condition, and prove it.
#
# Usage: scenarios/conditions/apply.sh baseline|degraded|remediated
#
#   baseline    the Alloy pipeline from deploy/kind/values/alloy.yaml, unchanged
#   degraded    baseline plus degraded.alloy (broken propagation, logs without
#               trace context)
#   remediated  baseline plus remediated.alloy (Argus's own resource-attributes
#               patch)
#
# The condition is a stage inserted between the OTLP receiver and the batch
# processor. Applying one is a `helm upgrade` of Alloy; the script then waits
# until the condition is visible in the telemetry itself, the same way a
# scenario's steady-state gate works, and fails if it never is. The condition
# is recorded in the ConfigMap lgtm/argus-bench-condition.
#
# Then label the runs to match: argus bench run --condition <name> ...
#
# Run from WSL (where `make dev-up` runs): it needs helm with the grafana repo,
# and kubectl pointed at the kind cluster (ARGUS_KUBE_CONTEXT to choose one).
set -euo pipefail

cond="${1:-}"
case "$cond" in
  baseline | degraded | remediated) ;;
  *)
    echo "usage: $0 baseline|degraded|remediated" >&2
    exit 2
    ;;
esac

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
base="$root/deploy/kind/values/alloy.yaml"
ctx=()
helm_ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then
  ctx=(--context "$ARGUS_KUBE_CONTEXT")
  helm_ctx=(--kube-context "$ARGUS_KUBE_CONTEXT")
fi
k() { kubectl "${ctx[@]}" "$@"; }

chart_version="$(grep -oE 'ALLOY_CHART_VERSION:-[0-9.]+' "$root/deploy/kind/bootstrap.sh" | head -1 | sed 's/.*:-//')"
[ -n "$chart_version" ] || { echo "could not read ALLOY_CHART_VERSION from bootstrap.sh" >&2; exit 1; }

values="$(mktemp)"
trap 'rm -f "$values"' EXIT
receiver_line='= [otelcol.processor.batch.default.input]'

if [ "$cond" = baseline ]; then
  cp "$base" "$values"
else
  snippet="$here/$cond.alloy"
  entry="$(sed -n 's|^// entry: *||p' "$snippet" | head -1)"
  [ -n "$entry" ] || { echo "$snippet declares no '// entry:' component" >&2; exit 1; }
  # Only the receiver's three outputs point at the batch processor; route them
  # through the condition instead. Anything else matching would be a surprise.
  n="$(grep -cF "$receiver_line" "$base")"
  [ "$n" = 3 ] || { echo "expected 3 receiver outputs to rewire in $base, found $n" >&2; exit 1; }
  sed 's|= \[otelcol\.processor\.batch\.default\.input\]|= ['"$entry"'.input]|' "$base" > "$values"
  # The config is the last key of the values file, a block scalar indented by 6.
  printf '\n' >> "$values"
  sed 's/^/      /' "$snippet" >> "$values"
fi

# The chart is downloaded once and reused. `helm upgrade grafana/alloy` fetches
# it from GitHub on every call, and one timed-out download aborted a matrix
# run; a 144-run matrix switches condition several times over many hours.
cache="${XDG_CACHE_HOME:-$HOME/.cache}/argus/charts"
chart="$cache/alloy-$chart_version.tgz"
if [ ! -s "$chart" ]; then
  mkdir -p "$cache"
  for attempt in 1 2 3; do
    if helm pull grafana/alloy --version "$chart_version" --destination "$cache" >/dev/null 2>&1 && [ -s "$chart" ]; then
      break
    fi
    [ "$attempt" = 3 ] && { echo "could not download the alloy $chart_version chart after 3 attempts" >&2; exit 1; }
    sleep $((attempt * 10))
  done
fi

echo "==> alloy: applying the $cond condition (chart $chart_version)"
helm upgrade alloy "$chart" -n lgtm -f "$values" "${helm_ctx[@]}" \
  --wait --timeout 5m >/dev/null
k -n lgtm rollout status ds/alloy --timeout=5m >/dev/null
k -n lgtm create configmap argus-bench-condition --from-literal=condition="$cond" \
  --dry-run=client -o yaml | k apply -f - >/dev/null

# --- prove it -------------------------------------------------------------
# Instant queries through the API server's service proxy: no port-forward.
#
# query prints the value of a PromQL expression, or nothing for an empty
# result. It FAILS when Mimir did not answer. Without that, a backend that is
# down reads as "no service-graph edges", which is exactly what proves the
# degraded condition; and without the timeout, a backend that never answers
# hung this script forever.
query() {
  local enc out
  enc="$(printf '%s' "$1" | sed 's/%/%25/g; s/ /%20/g; s/"/%22/g; s/{/%7B/g; s/}/%7D/g; s/\[/%5B/g; s/\]/%5D/g; s/!/%21/g; s/~/%7E/g; s/=/%3D/g; s/|/%7C/g; s/+/%2B/g; s/>/%3E/g; s/</%3C/g')"
  out="$(k get --request-timeout=20s --raw \
    "/api/v1/namespaces/lgtm/services/mimir-gateway:80/proxy/prometheus/api/v1/query?query=$enc" 2>/dev/null)" || return 1
  case "$out" in
    *'"status":"success"'*) ;;
    *) return 1 ;;
  esac
  printf '%s' "$out" | sed -n 's/.*"value":\[[^,]*,"\([^"]*\)".*/\1/p'
}

# Service-to-service edges the degradation removes (every server except
# checkout and payment), and resource attributes only the remediation adds,
# over the last 2 minutes. connection_type="" keeps real service pairs and
# leaves out Tempo's virtual edges ("user -> X", created for every root span)
# and database edges, which a broken propagation does not remove.
edges='sum(rate(traces_service_graph_request_total{connection_type="",server!~"checkout|payment"}[2m]))'
k8s_ids='count(count_over_time(target_info{k8s_deployment_name!=""}[2m]))'

observed() {
  local e ids
  e="$(query "$edges")" || return 1
  ids="$(query "$k8s_ids")" || return 1
  case "$cond" in
    degraded) awk -v e="${e:-0}" 'BEGIN { exit !(e < 0.01) }' ;;
    remediated) [ -n "$ids" ] && awk -v e="${e:-0}" 'BEGIN { exit !(e > 0.01) }' ;;
    baseline) [ -z "$ids" ] && awk -v e="${e:-0}" 'BEGIN { exit !(e > 0.01) }' ;;
  esac
}

echo "==> waiting for the $cond condition to show in the telemetry (up to 8m)"
for _ in $(seq 1 32); do
  if observed; then
    e="$(query "$edges" || echo '?')"
    ids="$(query "$k8s_ids" || echo '?')"
    echo "$cond condition is live: service-to-service edges ${e:-0}/s, services with k8s identity ${ids:-0}"
    exit 0
  fi
  sleep 15
done
echo "the $cond condition did not show in the telemetry within 8m" \
  "(edges=$(query "$edges" || echo 'Mimir did not answer'), k8s identities=$(query "$k8s_ids" || echo 'Mimir did not answer'))" >&2
exit 1
