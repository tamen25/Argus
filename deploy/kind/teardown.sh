#!/usr/bin/env bash
# Delete the Argus kind dev cluster. WARNING: destroys accumulated telemetry
# history (Mimir blocks). Only run when you accept losing it — history feeds
# Phase 3 backtests (master plan §9).
set -euo pipefail
CLUSTER_NAME="argus"

read -r -p "Delete kind cluster '${CLUSTER_NAME}' AND its accumulated history? [y/N] " ans
case "${ans}" in
  y|Y|yes|YES) kind delete cluster --name "${CLUSTER_NAME}" ;;
  *) echo "aborted"; exit 1 ;;
esac
