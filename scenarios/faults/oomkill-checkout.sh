#!/usr/bin/env bash
# Fault for oomkill-checkout: make the kernel OOM-kill checkout itself.
#
# An earlier version ran a memory stressor next to checkout (Chaos Mesh
# StressChaos). The OOM killer picks the largest process in the cgroup — the
# stressor — so checkout survived, never restarted, and the scenario never
# fired. This lowers checkout's own memory limit below its working set, which
# produces a genuine OOMKilled / CrashLoopBackOff on the real workload.
#
# Two details make it bite:
# - With one replica, a RollingUpdate keeps the old healthy pod running until the
#   new one is Ready, and a crashlooping pod never is. The strategy is switched
#   to Recreate so the healthy pod is replaced, not kept alongside.
# - The original limit and strategy are recorded as annotations ON THE
#   DEPLOYMENT before anything changes, so restore-checkout-memory.sh can put
#   back exactly what was there, and knows whether there is anything to restore.
#
# Run by `argus bench run --inject=auto` (ARGUS_NAMESPACE, ARGUS_KUBE_CONTEXT).
set -euo pipefail

ns="${ARGUS_NAMESPACE:-otel-demo}"
[ -n "$ns" ] || ns=otel-demo
ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then ctx=(--context "$ARGUS_KUBE_CONTEXT"); fi
k() { kubectl "${ctx[@]}" -n "$ns" "$@"; }

LIMIT_KEY=argus-oomkill-original-limit
STRATEGY_KEY=argus-oomkill-original-strategy
FAULT_LIMIT=8Mi

# Record originals once. If they are already recorded, a previous injection was
# never restored — keep the TRUE originals rather than recording the fault.
if [ -z "$(k get deploy checkout -o jsonpath="{.metadata.annotations.${LIMIT_KEY}}")" ]; then
  orig_limit="$(k get deploy checkout -o jsonpath='{.spec.template.spec.containers[?(@.name=="checkout")].resources.limits.memory}')"
  orig_strategy="$(k get deploy checkout -o jsonpath='{.spec.strategy}')"
  if [ -z "$orig_limit" ] || [ -z "$orig_strategy" ]; then
    echo "could not read checkout's original memory limit or strategy; refusing to inject" >&2
    exit 1
  fi
  k annotate deploy checkout "${LIMIT_KEY}=${orig_limit}" "${STRATEGY_KEY}=${orig_strategy}" --overwrite >/dev/null
fi

k patch deploy checkout --type=strategic -p \
  "{\"spec\":{\"strategy\":{\"type\":\"Recreate\",\"rollingUpdate\":null},\"template\":{\"spec\":{\"containers\":[{\"name\":\"checkout\",\"resources\":{\"limits\":{\"memory\":\"${FAULT_LIMIT}\"}}}]}}}}" >/dev/null

# Return only once the kernel has actually killed checkout, so the fault is
# established, not merely requested. Fail loudly if it never happens.
for _ in $(seq 1 45); do
  reasons="$(k get pods -l app.kubernetes.io/name=checkout \
    -o jsonpath='{.items[*].status.containerStatuses[*].lastState.terminated.reason}')"
  if [[ "$reasons" == *OOMKilled* ]]; then
    echo "checkout OOMKilled at ${FAULT_LIMIT}"
    exit 0
  fi
  sleep 4
done
echo "checkout was not OOMKilled within 3m at ${FAULT_LIMIT} (last reasons: ${reasons:-none})" >&2
exit 1
