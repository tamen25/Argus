#!/usr/bin/env bash
# Argus dev environment bootstrap (Phase 0 deliverable, master plan §9).
# One command: kind cluster + LGTM stack + OpenTelemetry Demo + Chaos Mesh.
# Idempotent: safe to re-run; every install is `helm upgrade --install`.
#
# Usage: bash deploy/kind/bootstrap.sh   (or: make dev-up)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="argus"

# Chart version pins — reproducible bootstraps. Re-pin deliberately, record
# notable bumps in DECISIONS.md. (Initial pins: latest at scaffold time.)
MIMIR_CHART_VERSION="${MIMIR_CHART_VERSION:-6.1.0}"
LOKI_CHART_VERSION="${LOKI_CHART_VERSION:-7.0.0}"
TEMPO_CHART_VERSION="${TEMPO_CHART_VERSION:-1.24.4}"
GRAFANA_CHART_VERSION="${GRAFANA_CHART_VERSION:-10.5.15}"
ALLOY_CHART_VERSION="${ALLOY_CHART_VERSION:-1.10.1}"
OTEL_DEMO_CHART_VERSION="${OTEL_DEMO_CHART_VERSION:-0.40.9}"
CHAOS_MESH_CHART_VERSION="${CHAOS_MESH_CHART_VERSION:-2.8.3}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: '$1' not found in PATH" >&2; exit 1; }; }
for tool in docker kind kubectl helm; do need "$tool"; done

echo "==> durable history dir (survives cluster recreation)"
mkdir -p /var/lib/argus/history

echo "==> kind cluster '${CLUSTER_NAME}'"
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  echo "    already exists, skipping create"
else
  kind create cluster --config "${SCRIPT_DIR}/kind-config.yaml"
fi
kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null

echo "==> helm repos"
helm repo add grafana https://grafana.github.io/helm-charts >/dev/null
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts >/dev/null
helm repo add chaos-mesh https://charts.chaos-mesh.org >/dev/null
helm repo update >/dev/null

for ns in lgtm otel-demo chaos-mesh; do
  kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done

echo "==> history PV/PVC (MinIO data on host mount)"
kubectl apply -f "${SCRIPT_DIR}/argus-history-pv.yaml"

echo "==> Mimir (365d retention — backtest history accumulation starts NOW)"
helm upgrade --install mimir grafana/mimir-distributed \
  --version "${MIMIR_CHART_VERSION}" -n lgtm \
  -f "${SCRIPT_DIR}/values/mimir.yaml" --timeout 15m

echo "==> Loki"
helm upgrade --install loki grafana/loki \
  --version "${LOKI_CHART_VERSION}" -n lgtm \
  -f "${SCRIPT_DIR}/values/loki.yaml" --timeout 10m

echo "==> Tempo"
helm upgrade --install tempo grafana/tempo \
  --version "${TEMPO_CHART_VERSION}" -n lgtm \
  -f "${SCRIPT_DIR}/values/tempo.yaml" --timeout 10m

echo "==> Grafana"
helm upgrade --install grafana grafana/grafana \
  --version "${GRAFANA_CHART_VERSION}" -n lgtm \
  -f "${SCRIPT_DIR}/values/grafana.yaml" --timeout 10m

echo "==> Alloy (OTLP gateway)"
helm upgrade --install alloy grafana/alloy \
  --version "${ALLOY_CHART_VERSION}" -n lgtm \
  -f "${SCRIPT_DIR}/values/alloy.yaml" --timeout 10m
kubectl apply -f "${SCRIPT_DIR}/alloy-nodeport.yaml"

echo "==> OpenTelemetry Demo (workload)"
helm upgrade --install otel-demo open-telemetry/opentelemetry-demo \
  --version "${OTEL_DEMO_CHART_VERSION}" -n otel-demo \
  -f "${SCRIPT_DIR}/values/otel-demo.yaml" --timeout 15m

echo "==> Chaos Mesh"
helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh \
  --version "${CHAOS_MESH_CHART_VERSION}" -n chaos-mesh \
  -f "${SCRIPT_DIR}/values/chaos-mesh.yaml" --timeout 10m

echo "==> waiting for workloads (this can take a few minutes on first run)"
kubectl -n lgtm rollout status deploy/grafana --timeout=10m
kubectl -n lgtm rollout status sts/loki --timeout=10m || true
kubectl -n lgtm rollout status sts/mimir-ingester --timeout=15m || true

cat <<'EOF'

Argus dev environment is up.

  Grafana        http://localhost:3000   (admin / argus-dev)
  OTLP gRPC      localhost:4317          (Alloy)
  OTLP HTTP      localhost:4318          (Alloy)
  In-cluster     alloy.lgtm.svc:4317|4318

REMINDER (master plan §9): history accumulation started. Log every induced
fault or incident in incidents.yaml at repo root — Phase 3 backtests depend
on it. Tear down with: make dev-down
EOF
