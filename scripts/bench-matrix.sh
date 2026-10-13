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
# §12.4 requires the projection to be confirmed before a full run, and a run
# can be a paid API call. Options:
#   --scenarios "a b c"   scenario names (default: every scenario in scenarios/)
#   --conditions "x y"    telemetry conditions to run (default: "degraded remediated")
#   --compare a,b         the comparison to write (default: degraded,remediated)
#   --repeats N           repeats per scenario (default 3)
#   --pass N              which pass this is (default 1); see "Sessions" below
#   --stop-after MIN      start no new cell after MIN minutes (default: no limit)
#   --max-tool-calls N    per-run tool-call cap (default 20)
#   --max-tokens N        per-run token cap (default 100000)
#
# Each condition is applied with scenarios/conditions/apply.sh, which proves it
# took effect; the cluster is put back to baseline at the end, even on failure.
# A run that fails is recorded in its report and the matrix carries on.
#
# Layout of --out:
#   reports/pass-<N>/<condition>__<agent>__<scenario>.json   one run report per cell
#   logs/pass-<N>/<condition>__<agent>__<scenario>.log       that invocation's stderr
#   comparison.md, comparison.json                           over every pass so far
#
# Sessions. The matrix need not run in one sitting. A cell whose report exists
# is skipped, so the same command resumes where the last one stopped, and
# --stop-after ends a session on time (the cell running then finishes first).
# Repeats can be added in passes rather than all at once: --repeats 1 --pass 1,
# later --repeats 1 --pass 2, and so on. The comparison pools every pass. One
# condition per session (--conditions degraded) avoids a condition switch, and
# the comparison is written once both conditions have reports.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
agents_file="" out="" yes=false
scenarios="" conditions="degraded remediated" compare="degraded,remediated"
repeats=3 pass=1 stop_after=0 max_calls=20 max_tokens=100000

while [ $# -gt 0 ]; do
  case "$1" in
    --agents) agents_file="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --scenarios) scenarios="$2"; shift 2 ;;
    --conditions) conditions="$2"; shift 2 ;;
    --compare) compare="$2"; shift 2 ;;
    --repeats) repeats="$2"; shift 2 ;;
    --pass) pass="$2"; shift 2 ;;
    --stop-after) stop_after="$2"; shift 2 ;;
    --max-tool-calls) max_calls="$2"; shift 2 ;;
    --max-tokens) max_tokens="$2"; shift 2 ;;
    --yes) yes=true; shift ;;
    -h | --help) sed -n '2,46p' "$0"; exit 0 ;;
    *) echo "unknown option $1 (see --help)" >&2; exit 2 ;;
  esac
done
if [ -z "$agents_file" ] || [ -z "$out" ]; then
  echo "--agents and --out are required (see --help)" >&2
  exit 2
fi
[ -f "$agents_file" ] || { echo "agents file $agents_file not found" >&2; exit 2; }
for n in "$repeats" "$pass" "$stop_after"; do
  [[ "$n" =~ ^[0-9]+$ ]] || { echo "--repeats, --pass and --stop-after take whole numbers" >&2; exit 2; }
done
[[ "$compare" =~ ^[a-z0-9-]+,[a-z0-9-]+$ ]] || { echo "--compare takes two conditions, a,b" >&2; exit 2; }

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
reports="$out/reports/pass-$pass"
logs="$out/logs/pass-$pass"

# Projection from the first real runs (docs/bench/first-real-run.md): a run is
# about 4 to 5.5 minutes of wall clock and 50k to 110k tokens on a model served
# entirely from GPU memory; a model partly offloaded to CPU is several times
# slower. Each run is capped at --max-tokens, plus at most one final turn after
# the budget notice (which may cross the cap, so the ceiling allows 10% over).
per_run_hi=$(( max_tokens * 11 / 10 ))
[ "$per_run_hi" -lt 110000 ] || per_run_hi=110000
# Durations in minutes and token counts are printed at a scale that does not
# round a single session down to zero.
mins() { if [ "$1" -lt 120 ]; then echo "$1 minutes"; else echo "$(( $1 / 60 )) hours"; fi; }
toks() { if [ "$1" -lt 1000000 ]; then echo "$(( $1 / 1000 ))k"; else echo "$(( $1 / 1000000 ))M"; fi; }
echo "Plan (pass $pass)"
echo "  scenarios:  ${#scenario_list[@]} (${scenario_list[*]})"
echo "  agents:     ${#labels[@]} (${labels[*]})"
echo "  conditions: ${#condition_list[@]} (${condition_list[*]})"
echo "  repeats:    $repeats"
echo "  runs:       $n_runs"
[ "$stop_after" -gt 0 ] && echo "  session:    no new cell after $stop_after minutes; run the same command again to resume"
echo "Projected cost"
echo "  wall clock: about $(mins $(( n_runs * 4 ))) to $(mins $(( n_runs * 11 / 2 ))) on a GPU-resident model, sequential, plus about 10 minutes per condition switch"
echo "  tokens:     about $(toks $(( n_runs * 50000 )))-$(toks $(( n_runs * per_run_hi ))) in total, $(toks $(( n_runs * 50000 / ${#labels[@]} )))-$(toks $(( n_runs * per_run_hi / ${#labels[@]} ))) per agent"
echo "  ceiling:    $(toks $(( n_runs * max_tokens * 11 / 10 ))) tokens ($max_tokens per run enforced by bench run, plus one final turn)"
echo "  Multiply each agent's tokens by its provider's price per token. EKS hours: 0 (this runs on kind)."
if ! $yes; then
  echo
  echo "Nothing was run. Check the projection, then add --yes to start."
  exit 0
fi

command -v argus >/dev/null || { echo "argus not on PATH (go build -o ~/go/bin/argus ./engine/cmd/argus)" >&2; exit 1; }
mkdir -p "$reports" "$logs"
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

started=$SECONDS
failed=0 ran=0 left=0
out_of_time() { [ "$stop_after" -gt 0 ] && [ $(( (SECONDS - started) / 60 )) -ge "$stop_after" ]; }

for cond in "${condition_list[@]}"; do
  # A condition whose cells all have reports needs no switch.
  todo=0
  for label in "${labels[@]}"; do
    for sc in "${scenario_list[@]}"; do
      [ -s "$reports/${cond}__${label}__${sc}.json" ] || todo=$((todo + 1))
    done
  done
  if [ "$todo" = 0 ]; then
    echo "==> condition: $cond (every cell has a report)"
    continue
  fi
  if out_of_time; then
    left=$((left + todo))
    continue
  fi
  echo "==> condition: $cond"
  bash "$root/scenarios/conditions/apply.sh" "$cond"
  for i in "${!labels[@]}"; do
    for sc in "${scenario_list[@]}"; do
      name="${cond}__${labels[$i]}__${sc}"
      report="$reports/$name.json"
      if [ -s "$report" ]; then
        echo "    skip  ${labels[$i]} × $sc (report exists)"
        continue
      fi
      if out_of_time; then
        left=$((left + 1))
        continue
      fi
      echo "    run   ${labels[$i]} × $sc ($(date +%H:%M))"
      ran=$((ran + 1))
      # Word splitting of the agent's flags is intended: they are one line of
      # options from the agents file.
      # shellcheck disable=SC2086
      if ! argus bench run --scenario "$root/scenarios/$sc.yaml" ${flags[$i]} \
        --mimir-url http://127.0.0.1:18080 --loki-url http://127.0.0.1:13100 --tempo-url http://127.0.0.1:13200 \
        --inject-namespace otel-demo "${ctx[@]}" \
        --repeats "$repeats" --max-tool-calls "$max_calls" --max-tokens "$max_tokens" \
        --condition "$cond" --env-digest "kind-$(date +%F)" \
        --format json --out "$report" 2>"$logs/$name.log"; then
        failed=$((failed + 1))
        echo "    FAILED (see $logs/$name.log)" >&2
      fi
    done
  done
done

# The comparison pools every pass so far, and needs both of its conditions.
a="${compare%,*}" b="${compare#*,}"
has() { compgen -G "$out/reports/pass-*/$1__*.json" >/dev/null; }
if has "$a" && has "$b"; then
  echo "==> comparison ($compare, every pass)"
  argus bench report --compare "$compare" "$out"/reports/pass-* --out "$out/comparison.md"
  argus bench report --compare "$compare" "$out"/reports/pass-* --format json --out "$out/comparison.json"
else
  echo "==> comparison skipped: it needs reports under both $a and $b"
fi
echo "matrix pass $pass: $ran cells run this session, $failed failed, $left left for the next session"
