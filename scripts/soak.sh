#!/usr/bin/env bash
# Soak harness: run the argus engine in-cluster against the live Alloy mirror
# and record everything needed to (a) validate bounded memory / window
# rotation over hours and (b) feed `argus rules calibrate` with real
# distributions.
#
# Usage:   SOAK_HOURS=24 ./scripts/soak.sh
# Output:  soak-output/<UTC timestamp>/
#   metrics.csv          one row per minute: engine self-metrics
#   report-NNN.json      hourly /api/report snapshot
#   aggregates-NNN.json  hourly /api/aggregates snapshot
#   engine-errors.log    hourly kubectl-logs error grep (receiver errors)
#
# Success criteria (see docs/soak.md): flat RSS after warmup, pairs_tracked
# sawtooths at window boundaries without monotonic growth, zero receiver
# errors, no engine restarts.
set -euo pipefail

# ARGUS_ROOT override lets the script run from a copied location (long runs
# shouldn't execute a file that git branch switches rewrite underneath bash).
ROOT="${ARGUS_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SOAK_HOURS="${SOAK_HOURS:-24}"
SAMPLE_SECONDS="${SAMPLE_SECONDS:-60}"
CLUSTER="${CLUSTER:-argus}"
OUT="${SOAK_OUT:-$ROOT/soak-output/$(date -u +%Y%m%d-%H%M%S)}"
PROXY="/api/v1/namespaces/argus/services/argus-engine:8080/proxy"

mkdir -p "$OUT"
echo "soak: output -> $OUT (${SOAK_HOURS}h, sample every ${SAMPLE_SECONDS}s)"

# --- stand up the engine + mirror (idempotent) -----------------------------
echo "soak: building engine image"
docker build -q -t argus-engine:dev \
  --build-arg SPEC_VERSION="$(cat "$ROOT/.instrumentation-score-version")" \
  --build-arg VERSION="soak-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo dev)" \
  "$ROOT/engine"
kind load docker-image argus-engine:dev --name "$CLUSTER"

kubectl apply -f "$ROOT/deploy/kind/argus-engine.yaml"
# pick up a freshly loaded image even if the tag didn't change
kubectl rollout restart deployment/argus-engine -n argus
kubectl rollout status deployment/argus-engine -n argus --timeout=120s

echo "soak: wiring alloy mirror (helm upgrade with repo values)"
ALLOY_CHART_VERSION="$(grep -oP 'ALLOY_CHART_VERSION:-\K[0-9.]+' "$ROOT/deploy/kind/bootstrap.sh" || true)"
helm upgrade alloy grafana/alloy -n lgtm \
  ${ALLOY_CHART_VERSION:+--version "$ALLOY_CHART_VERSION"} \
  -f "$ROOT/deploy/kind/values/alloy.yaml" >/dev/null
kubectl rollout status daemonset/alloy -n lgtm --timeout=180s

# --- sampling loop ----------------------------------------------------------
metric() { # metric <scrape-file> <name> -> last value (label-insensitive sum)
  awk -v m="$2" '$1 ~ "^"m"({|$| )" {sum += $NF} END {printf "%.0f", sum}' "$1"
}

echo "ts,rss_bytes,go_heap_bytes,pairs_tracked,evictions_total,items_traces,items_metrics,items_logs,goroutines" > "$OUT/metrics.csv"

TOTAL_MINUTES=$(( SOAK_HOURS * 60 ))
for (( i=0; i<TOTAL_MINUTES; i++ )); do
  TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  SCRAPE="$OUT/.scrape.tmp"
  if kubectl get --raw "$PROXY/metrics" > "$SCRAPE" 2>>"$OUT/harness.log"; then
    echo "$TS,$(metric "$SCRAPE" process_resident_memory_bytes),$(metric "$SCRAPE" go_memstats_heap_alloc_bytes),$(metric "$SCRAPE" argus_aggregate_pairs_tracked),$(metric "$SCRAPE" argus_aggregate_pair_evictions_total),$(awk '$1 ~ /^argus_items_consumed_total{signal="traces"}/ {print $NF}' "$SCRAPE"),$(awk '$1 ~ /^argus_items_consumed_total{signal="metrics"}/ {print $NF}' "$SCRAPE"),$(awk '$1 ~ /^argus_items_consumed_total{signal="logs"}/ {print $NF}' "$SCRAPE"),$(metric "$SCRAPE" go_goroutines)" >> "$OUT/metrics.csv"
  else
    echo "$TS scrape failed" >> "$OUT/harness.log"
  fi

  if (( i % 60 == 0 )); then
    N=$(printf '%03d' $(( i / 60 )))
    kubectl get --raw "$PROXY/api/report"     > "$OUT/report-$N.json"     2>>"$OUT/harness.log" || true
    kubectl get --raw "$PROXY/api/aggregates" > "$OUT/aggregates-$N.json" 2>>"$OUT/harness.log" || true
    kubectl logs -n argus deployment/argus-engine --since=61m 2>/dev/null \
      | grep -iE 'error|panic|drop' >> "$OUT/engine-errors.log" || true
    echo "soak: hour $N snapshot done ($TS)"
  fi
  sleep "$SAMPLE_SECONDS"
done

# final snapshot
N=$(printf '%03d' $(( TOTAL_MINUTES / 60 )))
kubectl get --raw "$PROXY/api/report"     > "$OUT/report-$N.json"     || true
kubectl get --raw "$PROXY/api/aggregates" > "$OUT/aggregates-$N.json" || true
kubectl get pods -n argus -o wide > "$OUT/final-pods.txt" || true
rm -f "$OUT/.scrape.tmp"
echo "soak: done -> $OUT"
echo "soak: analyze with: make soak-analyze SOAK_DIR=$OUT"
