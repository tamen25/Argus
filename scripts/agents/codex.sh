#!/usr/bin/env bash
# OpenAI's Codex CLI as an Argus bench agent, through the shell adapter:
#
#   argus bench run --agent shell --shell-command scripts/agents/codex.sh \
#     --agent-name codex --model <the model Codex uses> ...
#
# Codex investigates with Argus's own read-only tools (`argus mcp`, the same
# surface the API agents get), inside a read-only sandbox with no network, so
# the telemetry tools are its only window on the incident. It prints the
# diagnosis as the JSON Argus scores; this script extracts that object from
# its last message and writes it to stdout.
#
# Isolation: a read-only sandbox can still READ any file, and the scenario
# files hold the ground truth. Set ARGUS_AGENT_USER to run Codex as a Unix user
# that cannot read the checkout (the bench VM, deploy/terraform/bench-vm, does).
# Without it the run is not isolated, and the script says so on stderr.
#
# Environment, beyond ARGUS_BRIEF and ARGUS_SCENARIO from the shell adapter:
#   ARGUS_MCP_ARGS    arguments for `argus mcp`: backend URLs, --kube-topology
#   CODEX_MODEL       model to request (default: Codex's own default)
#   ARGUS_AGENT_USER  Unix user to run Codex as (passwordless sudo to it)
#
# Budgets: Argus cannot count a shell agent's tokens or tool calls; the run is
# capped by wall clock only (--agent-timeout), and reports say so.
set -euo pipefail

: "${ARGUS_BRIEF:?the shell adapter sets ARGUS_BRIEF}"
argus_bin="$(command -v argus)" || { echo "codex.sh: argus not on PATH" >&2; exit 1; }
command -v codex >/dev/null || { echo "codex.sh: codex not on PATH" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
# Codex, possibly as another user, writes its last message here.
chmod 777 "$work"

prompt="${ARGUS_BRIEF}

You are being benchmarked on this diagnosis. Investigate only with the argus
MCP tools (metrics, logs, traces, alerts, topology). Do not run shell commands
or read local files: they are not part of the incident.

When you are done, reply with ONLY one JSON object, no prose and no code fence:
{\"root_cause_entities\":[{\"kind\":\"Deployment\",\"namespace\":\"<namespace>\",\"name\":\"<workload>\"}],
 \"category\":\"<exactly one of the listed category names>\",
 \"evidence\":[{\"signal\":\"<metrics|logs|traces|alerts|topology>\",\"query\":\"<the tool call or query>\",\"observation\":\"<what you saw>\"}],
 \"summary\":\"<one paragraph>\",
 \"confidence\":<a number from 0 to 1>}"

# `argus mcp` with the backends, as a TOML array for codex's -c override.
read -r -a mcp_args <<< "${ARGUS_MCP_ARGS:-}"
args_toml='["mcp"'
for a in "${mcp_args[@]}"; do
  args_toml+=",\"${a}\""
done
args_toml+=']'

cmd=(codex exec --skip-git-repo-check --ephemeral --ignore-user-config
  --sandbox read-only -C "$work" -o "$work/last.txt"
  -c "mcp_servers.argus.command=\"${argus_bin}\""
  -c "mcp_servers.argus.args=${args_toml}")
[ -n "${CODEX_MODEL:-}" ] && cmd+=(-m "$CODEX_MODEL")

rc=0
if [ -n "${ARGUS_AGENT_USER:-}" ]; then
  # The redirects are this script's, into its own work dir: intended.
  # shellcheck disable=SC2024
  sudo -n -H -u "$ARGUS_AGENT_USER" -- "${cmd[@]}" "$prompt" >"$work/codex.out" 2>"$work/codex.err" || rc=$?
else
  echo "codex.sh: ARGUS_AGENT_USER is unset; Codex can read local files, scenario ground truth included" >&2
  "${cmd[@]}" "$prompt" >"$work/codex.out" 2>"$work/codex.err" || rc=$?
fi
if [ "$rc" != 0 ] || [ ! -s "$work/last.txt" ]; then
  echo "codex.sh: codex exited $rc without a final message; its last output:" >&2
  tail -n 20 "$work/codex.err" >&2
  exit 1
fi

# The diagnosis object: the whole message if it parses, else the content of a
# code fence, else the span from the first { to the last }. Anything else is
# passed through, and Argus's normalizer records why it is not a diagnosis.
python3 - "$work/last.txt" <<'PY'
import json, re, sys
text = open(sys.argv[1], encoding="utf-8").read().strip()
candidates = [text]
m = re.search(r"```(?:json)?\s*(.*?)```", text, re.S)
if m:
    candidates.append(m.group(1).strip())
if "{" in text and "}" in text:
    candidates.append(text[text.index("{"): text.rindex("}") + 1])
for c in candidates:
    try:
        obj = json.loads(c)
    except ValueError:
        continue
    if isinstance(obj, dict):
        print(json.dumps(obj))
        sys.exit(0)
print(text)
PY
