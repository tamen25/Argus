#!/usr/bin/env bash
# The flagship run matrix: every scenario × every agent × every telemetry
# condition, then the degraded-vs-remediated comparison.
#
# Usage (from WSL, where make dev-up runs):
#   scripts/bench-matrix.sh --agents agents.txt --out runs/flagship            # print the plan and cost estimate
#   scripts/bench-matrix.sh --agents agents.txt --out runs/flagship --yes      # run it
#
# agents.txt has one agent per line, "label | argus bench run flags":
#   claude-sonnet | --agent anthropic --model claude-sonnet-5-5 --api-key-env ANTHROPIC_API_KEY
#   gpt-budget    | --agent openai --endpoint https://api.example/v1/chat/completions --model m --api-key-env OPENAI_API_KEY
#   holmesgpt     | --agent shell --shell-command holmes --shell-arg ask
# Blank lines and lines starting with # are ignored.
#
# Without --yes it only prints the plan and the projected cost: master plan
# §12.4 requires the projection to be confirmed before a full run, and every
# run here is a paid API call. Options:
#   --scenarios "a b c"   scenario names (default: every scenario in scenarios/)
#   --conditions "x y"    telemetry conditions (default: "degraded remediated")
#   --repeats N           repeats per scenario (default 3)
#   --max-tool-calls N    per-run tool-call cap (default 20)
#   --max-tokens N        per-run token cap (default 100000)
#
# Each condition is applied with scenarios/conditions/apply.sh, which proves it
# took effect; the cluster is put back to baseline at the end, even on failure.
# A run that fails is recorded in its report and the matrix carries on. The
# reports go to <out>/<condition>__<agent>__<scenario>.json, and the comparison
# to <out>/comparison.md and <out>/comparison.json.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
agents_file="" out="" yes=false
scenarios="" conditions="degraded remediated" repeats=3 max_calls=20 max_tokens=100000

while [ $# -gt 0 ]; do
  case "$1" in
    --agents) agents_file="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --scenarios) scenarios="$2"; shift 2 ;;
    --conditions) conditions="$2"; shift 2 ;;
    --repeats) repeats="$2"; shift 2 ;;
    --max-tool-calls) max_calls="$2"; shift 2 ;;
    --max-tokens) max_tokens="$2"; shift 2 ;;
    --yes) yes=true; shift ;;
    -h | --help) sed -n '2,32p' "$0"; exit 0 ;;
    *) echo "unknown option $1 (see --help)" >&2; exit 2 ;;
  esac
done
if [ -z "$agents_file" ] || [ -z "$out" ]; then
  echo "--agents and --out are required (see --help)" >&2
  exit 2
fi
[ -f "$agents_file" ] || { echo "agents file $agents_file not found" >&2; exit 2; }

if [ -z "$scenarios" ]; then
  for f in "$root"/scenarios/*.yaml; do
    [ "$(basename "$f")" = categories.yaml ] && continue
    scenarios="$scenarios $(basename "$f" .yaml)"
  done
fi

labels=() flags=()
while IFS= read -r line || [ -n "$line" ]; do
  line="${line%%#*}"
  [ -n "${line// /}" ] || continue
  label="$(printf '%s' "${line%%|*}" | xargs)"
  flag="$(printf '%s' "${line#*|}" | xargs)"
  [[ "$label" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || { echo "agent label '$label': use lowercase letters, digits, dots and dashes" >&2; exit 2; }
  labels+=("$label")
  flags+=("$flag")
done < "$agents_file"
[ "${#labels[@]}" -gt 0 ] || { echo "no agents in $agents_file" >&2; exit 2; }

read -r -a scenario_list <<< "$scenarios"
read -r -a condition_list <<< "$conditions"
n_runs=$(( ${#scenario_list[@]} * ${#labels[@]} * ${#condition_list[@]} * repeats ))

# Projection from the first real runs (docs/bench/first-real-run.md): a run is
# about 4 to 5.5 minutes of wall clock and 50k to 110k tokens. Each run is
# capped at --max-tokens, plus at most one final turn after the budget notice
# (which may cross the cap, so the ceiling allows 10% over).
per_run_hi=$(( max_tokens * 11 / 10 ))
[ "$per_run_hi" -lt 110000 ] || per_run_hi=110000
echo "Plan"
echo "  scenarios:  ${#scenario_list[@]} (${scenario_list[*]})"
echo "  agents:     ${#labels[@]} (${labels[*]})"
echo "  conditions: ${#condition_list[@]} (${condition_list[*]})"
echo "  repeats:    $repeats"
echo "  runs:       $n_runs"
echo "Projected cost"
echo "  wall clock: about $(( n_runs * 4 / 60 ))-$(( n_runs * 11 / 2 / 60 + 1 )) hours, sequential, plus about 10 minutes per condition switch"
echo "  tokens:     about $(( n_runs * 50 / 1000 ))-$(( n_runs * per_run_hi / 1000000 ))M in total, $(( n_runs * 50 / ${#labels[@]} / 1000 ))-$(( n_runs * per_run_hi / ${#labels[@]} / 1000000 ))M per agent"
echo "  ceiling:    $(( n_runs * max_tokens * 11 / 10 / 1000000 ))M tokens ($max_tokens per run enforced by bench run, plus one final turn)"
echo "  Multiply each agent's tokens by its provider's price per token. EKS hours: 0 (this runs on kind)."
if ! $yes; then
  echo
  echo "Nothing was run. Check the projection, then add --yes to start."
  exit 0
fi

command -v argus >/dev/null || { echo "argus not on PATH (go build -o ~/go/bin/argus ./engine/cmd/argus)" >&2; exit 1; }
mkdir -p "$out"
ctx=()
[ -n "${ARGUS_KUBE_CONTEXT:-}" ] && ctx=(--kube-context "$ARGUS_KUBE_CONTEXT")

# Backends for the agents' tools, through port-forwards that live as long as
# the matrix does.
pf_pids=()
cleanup() {
  for p in "${pf_pids[@]}"; do kill "$p" 2>/dev/null || true; done
  echo "==> restoring the baseline condition"
  bash "$root/scenarios/conditions/apply.sh" baseline || echo "WARNING: baseline not restored; run scenarios/conditions/apply.sh baseline" >&2
}
trap cleanup EXIT
kubectl port-forward -n lgtm svc/mimir-gateway 18080:80 >/dev/null 2>&1 & pf_pids+=($!)
kubectl port-forward -n lgtm svc/loki-gateway 13100:80 >/dev/null 2>&1 & pf_pids+=($!)
kubectl port-forward -n lgtm svc/tempo 13200:3200 >/dev/null 2>&1 & pf_pids+=($!)
for _ in $(seq 1 30); do
  curl -sf -o /dev/null "http://127.0.0.1:18080/prometheus/api/v1/query?query=up" && break
  sleep 2
done

failed=0
for cond in "${condition_list[@]}"; do
  echo "==> condition: $cond"
  bash "$root/scenarios/conditions/apply.sh" "$cond"
  for i in "${!labels[@]}"; do
    for sc in "${scenario_list[@]}"; do
      report="$out/${cond}__${labels[$i]}__${sc}.json"
      if [ -s "$report" ]; then
        echo "    skip  ${labels[$i]} × $sc (report exists)"
        continue
      fi
      echo "    run   ${labels[$i]} × $sc"
      # Word splitting of the agent's flags is intended: they are one line of
      # options from the agents file.
      # shellcheck disable=SC2086
      if ! argus bench run --scenario "$root/scenarios/$sc.yaml" ${flags[$i]} \
        --mimir-url http://127.0.0.1:18080 --loki-url http://127.0.0.1:13100 --tempo-url http://127.0.0.1:13200 \
        --inject-namespace otel-demo "${ctx[@]}" \
        --repeats "$repeats" --max-tool-calls "$max_calls" --max-tokens "$max_tokens" \
        --condition "$cond" --env-digest "kind-$(date +%F)" \
        --format json --out "$report" 2>"$out/${cond}__${labels[$i]}__${sc}.log"; then
        failed=$((failed + 1))
        echo "    FAILED (see ${report%.json}.log)" >&2
      fi
    done
  done
done

echo "==> comparison"
argus bench report --compare "$(echo "$conditions" | awk '{print $1","$2}')" "$out" --out "$out/comparison.md"
argus bench report --compare "$(echo "$conditions" | awk '{print $1","$2}')" "$out" --format json --out "$out/comparison.json"
echo "matrix done: $n_runs runs planned, $failed scenario invocations failed"
