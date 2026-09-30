#!/usr/bin/env bash
# Build the argus engine image from this checkout, load it into the kind
# cluster and (re)deploy it. Used by bootstrap.sh (make dev-up), by
# scripts/soak.sh, and on its own to pick up engine changes (make dev-engine).
# Idempotent: safe to re-run.
#
# The engine is read-only by design: it receives the Alloy sampled mirror and
# queries Mimir/Loki, with no credentials and no write paths (master plan §5).
#
# Usage: bash deploy/kind/deploy-engine.sh
#   CLUSTER          kind cluster name (default: argus)
#   ARGUS_VERSION    version string stamped into the binary
#                    (default: dev-<short commit>)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ARGUS_ROOT:-$(cd "${SCRIPT_DIR}/../.." && pwd)}"
CLUSTER="${CLUSTER:-argus}"
VERSION="${ARGUS_VERSION:-dev-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: '$1' not found in PATH" >&2; exit 1; }; }
for tool in docker kind kubectl; do need "$tool"; done

echo "==> argus engine image (${VERSION})"
docker build -q -t argus-engine:dev \
  --build-arg SPEC_VERSION="$(cat "$ROOT/.instrumentation-score-version")" \
  --build-arg VERSION="$VERSION" \
  "$ROOT/engine"
kind load docker-image argus-engine:dev --name "$CLUSTER"

echo "==> argus engine (namespace argus)"
kubectl apply -f "${SCRIPT_DIR}/argus-engine.yaml"
# The tag never changes, so restart to pick up a freshly loaded image.
kubectl rollout restart deployment/argus-engine -n argus
kubectl rollout status deployment/argus-engine -n argus --timeout=180s
