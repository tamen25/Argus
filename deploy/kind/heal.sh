#!/usr/bin/env bash
# Recover the dev cluster after a Docker Desktop restart (make dev-heal).
#
# The usual failure: Docker Desktop started before the Ubuntu WSL distro, so the
# kind node's history bind mount could not resolve and Docker put an empty
# directory in its place. MinIO then refuses to start (the sentinel guard in
# values/mimir.yaml) and every Mimir component that needs object storage
# crashloops behind it.
#
# The fix is to restart the node container once WSL is up — and this script only
# runs from WSL, so WSL is up by the time it does. It does not touch the history
# itself. Safe to re-run.
set -euo pipefail

NODE="argus-control-plane"
SENTINEL_IN_NODE="/data/argus-history/.argus-history-sentinel"
SENTINEL_ON_HOST="/var/lib/argus/history/.argus-history-sentinel"

need() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: '$1' not found in PATH" >&2; exit 1; }; }
for tool in docker kubectl; do need "$tool"; done

node_sees_history() { docker exec "$NODE" test -f "$SENTINEL_IN_NODE"; }

if ! docker inspect "$NODE" >/dev/null 2>&1; then
  echo "ERROR: kind node '$NODE' does not exist — run 'make dev-up'" >&2
  exit 1
fi

# Distinguish "mount detached" from "sentinel never created": the second is not a
# mount problem, and restarting the node would not fix it.
if [ ! -f "$SENTINEL_ON_HOST" ]; then
  echo "ERROR: $SENTINEL_ON_HOST is missing on the WSL host itself." >&2
  echo "       This is not a detached mount. If /var/lib/argus/history holds real" >&2
  echo "       history, recreate the sentinel by re-running 'make dev-up' (it is" >&2
  echo "       idempotent). If the directory is empty, the history is gone — restore" >&2
  echo "       it from a backup before creating anything (docs/history-durability.md)." >&2
  exit 1
fi

if node_sees_history; then
  echo "==> history mount attached (sentinel visible in the node)"
else
  echo "==> history mount DETACHED: the node sees an empty stand-in, not the real history"
  echo "==> restarting '$NODE' now that WSL is up, so Docker re-resolves the bind mount"
  docker restart "$NODE" >/dev/null
  for _ in $(seq 1 30); do
    node_sees_history && break
    sleep 2
  done
  if ! node_sees_history; then
    echo "ERROR: still detached after a restart. Check that Docker Desktop's WSL" >&2
    echo "       integration is enabled for Ubuntu-24.04, then retry." >&2
    exit 1
  fi
  echo "==> history mount re-attached"
fi

echo "==> waiting for the API server to authorize requests"
# A restarted control plane answers before it is usable: the RBAC authorizer
# denies requests ("Forbidden: User kubernetes-admin cannot get ...") until its
# caches sync. /readyz can pass before that, so wait on the exact authorized call
# the next step makes. Found by running this script against a real detach.
api_ready=false
for _ in $(seq 1 60); do
  if kubectl --context kind-argus -n lgtm get deploy mimir-minio >/dev/null 2>&1; then
    api_ready=true
    break
  fi
  sleep 5
done
if [ "$api_ready" != true ]; then
  echo "ERROR: the API server did not start authorizing requests within 5 minutes" >&2
  exit 1
fi

echo "==> waiting for the LGTM stack to recover"
# The node restart (or the earlier outage) leaves MinIO and Mimir restarting;
# give them a moment, then report rather than block forever.
kubectl --context kind-argus -n lgtm rollout status deploy/mimir-minio --timeout=5m
kubectl --context kind-argus -n lgtm rollout status sts/mimir-ingester --timeout=10m || true

not_ready="$(kubectl --context kind-argus get pods -A --no-headers 2>/dev/null | grep -vcE 'Running|Completed' || true)"
echo "==> pods not Running/Completed: ${not_ready}"
echo "    Mimir's label/metadata APIs may error for a while after an outage (stale"
echo "    bucket index — the compactor catches up); instant queries work meanwhile."
