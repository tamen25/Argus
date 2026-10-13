#!/usr/bin/env bash
# Drive the AWS bench VM (deploy/terraform/bench-vm) from your machine: the
# flagship matrix runs there unattended, so it does not need your PC to stay on.
#
#   scripts/bench-vm.sh up                 create the VM and bucket (terraform apply) — costs money from here
#   scripts/bench-vm.sh wait               wait until the VM has built the kind cluster (about 20 min)
#   scripts/bench-vm.sh login              copy your Codex login (~/.codex/auth.json) to the agent user
#   scripts/bench-vm.sh start [passes] [scenarios] [model]
#                                          start the flagship (default 3 passes, every scenario)
#   scripts/bench-vm.sh status             the run log's tail, and whether the run is done
#   scripts/bench-vm.sh fetch [dir]        copy results from S3 (default runs/bench-vm)
#   scripts/bench-vm.sh down               destroy everything, bucket included: fetch first
#
# Needs: terraform, the AWS CLI with credentials, python3 (or python).
# The VM has no inbound access; commands go through SSM Run Command.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
tf="$root/deploy/terraform/bench-vm"
py="$(command -v python3 || command -v python)"

out() { terraform -chdir="$tf" output -raw "$1"; }

# ssm <command...>: run a shell command on the VM as root and print its output.
ssm() {
  local iid region params id status
  iid="$(out instance_id)"
  region="$(out region)"
  params="$("$py" -c 'import json,sys; print(json.dumps({"commands": [sys.argv[1]], "executionTimeout": ["600"]}))' "$*")"
  id="$(aws ssm send-command --region "$region" --instance-ids "$iid" --document-name AWS-RunShellScript \
    --parameters "$params" --query Command.CommandId --output text)"
  for _ in $(seq 1 120); do
    status="$(aws ssm get-command-invocation --region "$region" --command-id "$id" --instance-id "$iid" \
      --query Status --output text 2>/dev/null || echo Pending)"
    case "$status" in Success | Failed | Cancelled | TimedOut) break ;; esac
    sleep 5
  done
  aws ssm get-command-invocation --region "$region" --command-id "$id" --instance-id "$iid" \
    --query StandardOutputContent --output text
  [ "$status" = Success ]
}

case "${1:-}" in
  up)
    terraform -chdir="$tf" init -input=false >/dev/null
    terraform -chdir="$tf" apply -input=false -auto-approve
    echo "VM created. It bills until 'down' (or until the run ends and it terminates itself)."
    ;;
  wait)
    echo "waiting for setup (tools, checkout, kind cluster) …"
    for _ in $(seq 1 90); do
      if ssm 'test -f /var/lib/argus-setup-done && cat /var/lib/argus-setup-done' 2>/dev/null; then
        echo "setup done"
        exit 0
      fi
      sleep 30
    done
    echo "setup not done after 45 min; see /var/log/argus-setup.log:" >&2
    ssm 'tail -n 40 /var/log/argus-setup.log' >&2 || true
    exit 1
    ;;
  login)
    # Through the bucket, not the command line: SSM keeps command parameters in
    # its history, and this is a login token. The object is deleted at once.
    auth="${CODEX_HOME:-$HOME/.codex}/auth.json"
    [ -s "$auth" ] || { echo "no Codex login at $auth: run 'codex login' first" >&2; exit 1; }
    bucket="$(out bucket)"
    aws s3 cp "$auth" "s3://$bucket/secrets/auth.json" --only-show-errors
    ssm "aws s3 cp s3://$bucket/secrets/auth.json /home/argusagent/.codex/auth.json --only-show-errors \
      && aws s3 rm s3://$bucket/secrets/auth.json --only-show-errors \
      && chown argusagent:argusagent /home/argusagent/.codex/auth.json \
      && chmod 600 /home/argusagent/.codex/auth.json \
      && sudo -u argusagent -H codex login status"
    ;;
  start)
    passes="${2:-3}" scenarios="${3:-}" model="${4:-}"
    bucket="$(out bucket)"
    ssm "nohup bash /root/argus/deploy/terraform/bench-vm/run-flagship.sh '$bucket' '$passes' '$scenarios' '$model' >/dev/null 2>&1 & echo started"
    ;;
  status)
    ssm 'tail -n 25 /root/runs/run.log 2>/dev/null; test -f /root/runs/DONE && echo "== DONE"; uptime'
    ;;
  fetch)
    dir="${2:-$root/runs/bench-vm}"
    aws s3 sync "s3://$(out bucket)/runs" "$dir" --region "$(out region)" --only-show-errors
    echo "results in $dir"
    ;;
  down)
    terraform -chdir="$tf" destroy -input=false -auto-approve
    echo "destroyed: nothing left billing"
    ;;
  *)
    sed -n '2,15p' "$0"
    exit 2
    ;;
esac
