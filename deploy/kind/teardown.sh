#!/usr/bin/env bash
# Delete the Argus kind dev cluster.
# Telemetry history SURVIVES this: Mimir blocks live in MinIO whose data dir is
# /var/lib/argus/history on the WSL host (kind extraMount). Recreating the
# cluster with `make dev-up` picks the history back up. Recent unflushed data
# (ingester/Kafka WAL, ~last 2h) is lost — see docs/history-durability.md.
set -euo pipefail
CLUSTER_NAME="argus"

read -r -p "Delete kind cluster '${CLUSTER_NAME}'? (history in /var/lib/argus/history is kept) [y/N] " ans
case "${ans}" in
  y|Y|yes|YES) kind delete cluster --name "${CLUSTER_NAME}" ;;
  *) echo "aborted"; exit 1 ;;
esac
