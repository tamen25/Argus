#!/usr/bin/env bash
# Runs on the bench VM (as root, started by scripts/bench-vm.sh start): the
# flagship matrix with Codex as the agent, pass after pass, unattended. After
# every pass the results go to S3; at the end the VM powers off, which
# terminates it (instance_initiated_shutdown_behavior).
#
# Usage: run-flagship.sh <bucket> [passes] [scenarios] [model]
#   passes     default 3
#   scenarios  space-separated names; default every scenario
#   model      what Codex runs, recorded in the reports (default: unpinned)
set -uo pipefail

bucket="${1:?bucket}"
passes="${2:-3}"
scenarios="${3:-}"
model="${4:-}"
root=/root/argus
out=/root/runs/flagship-codex
log=/root/runs/run.log
mkdir -p /root/runs
exec >>"$log" 2>&1
export PATH="$PATH:/usr/local/go/bin:/snap/bin"

echo "==> $(date -Is) flagship start: snapshot $(cat /var/lib/argus-snapshot), $passes pass(es)"

# Request metrics only exist once the demo has served traffic (CLAUDE.md,
# "Verify telemetry on a warm cluster"): wait until setup is 15 minutes old.
done_at=$(date -d "$(cat /var/lib/argus-setup-done)" +%s)
wait=$(( done_at + 900 - $(date +%s) ))
if [ "$wait" -gt 0 ]; then
  echo "==> warming up for $wait s"
  sleep "$wait"
fi

agents=/root/runs/agents.txt
model_flag=""
[ -n "$model" ] && model_flag="--model $model"
echo "codex | --agent shell --shell-command $root/scripts/agents/codex.sh --agent-name codex $model_flag --agent-timeout 20m" > "$agents"

export ARGUS_AGENT_USER=argusagent
export ARGUS_MCP_ARGS="--mimir-url http://127.0.0.1:18080 --loki-url http://127.0.0.1:13100 --tempo-url http://127.0.0.1:13200 --kube-topology"
[ -n "$model" ] && export CODEX_MODEL="$model"

scenario_args=()
[ -n "$scenarios" ] && scenario_args=(--scenarios "$scenarios")

for pass in $(seq 1 "$passes"); do
  echo "==> $(date -Is) pass $pass"
  "$root/scripts/bench-matrix.sh" --agents "$agents" --out "$out" --repeats 1 --pass "$pass" \
    --conditions "degraded remediated" "${scenario_args[@]}" --yes
  aws s3 sync /root/runs "s3://$bucket/runs" --only-show-errors
done

echo "==> $(date -Is) flagship done"
aws s3 sync /root/runs "s3://$bucket/runs" --only-show-errors
touch /root/runs/DONE
aws s3 cp /root/runs/DONE "s3://$bucket/runs/DONE" --only-show-errors
if [ "${ARGUS_NO_POWEROFF:-}" != 1 ]; then
  shutdown -h now
fi
