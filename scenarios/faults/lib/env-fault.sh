#!/usr/bin/env bash
# Shared helpers for faults that change ONE environment variable on ONE
# Deployment's container, and for restoring it. Sourced by the scripts in
# faults/; not run directly.
#
# The original env entry is recorded as an annotation ON THE DEPLOYMENT before
# anything changes:
#
#   argus-env-original-<VAR>: <container>|<original entry JSON, or __absent__>
#
# so a restore puts back exactly what was there — a literal value, a valueFrom
# reference (otel-demo sets OTEL_SERVICE_NAME through a downward-API fieldRef),
# or the variable's absence — and restore_all_env can repair every recorded
# mutation in the namespace, including one leaked by a run that crashed before
# its cleanup. The annotation is removed only after the restore's rollout
# succeeds, so a failed restore can simply run again. Re-injecting over an
# unrestored fault keeps the TRUE original rather than recording the fault.
#
# Uses ARGUS_NAMESPACE and ARGUS_KUBE_CONTEXT, set by `argus bench run
# --inject=auto`, so it targets the same cluster as the manifest steps.

_ns="${ARGUS_NAMESPACE:-otel-demo}"
[ -n "$_ns" ] || _ns=otel-demo
_ctx=()
if [ -n "${ARGUS_KUBE_CONTEXT:-}" ]; then _ctx=(--context "$ARGUS_KUBE_CONTEXT"); fi
_k() { kubectl "${_ctx[@]}" -n "$_ns" "$@"; }

_ENV_PREFIX="argus-env-original-"

# set_env DEPLOY CONTAINER VAR VALUE — record VAR's original entry, set VAR to a
# literal VALUE, and wait for the rollout.
set_env() {
  local deploy=$1 container=$2 var=$3 value=$4
  local key="${_ENV_PREFIX}${var}"
  if [ -z "$(_k get deploy "$deploy" -o jsonpath="{.metadata.annotations.${key}}")" ]; then
    local orig
    orig="$(_k get deploy "$deploy" -o jsonpath="{.spec.template.spec.containers[?(@.name==\"${container}\")].env[?(@.name==\"${var}\")]}")"
    [ -n "$orig" ] || orig=__absent__
    _k annotate deploy "$deploy" "${key}=${container}|${orig}" --overwrite >/dev/null
  fi
  _k patch deploy "$deploy" --type=strategic -p \
    "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"${container}\",\"env\":[{\"name\":\"${var}\",\"value\":\"${value}\",\"valueFrom\":null}]}]}}}}" >/dev/null
  _k rollout status "deploy/${deploy}" --timeout=5m >/dev/null
}

# restore_env DEPLOY VAR — put VAR's recorded original back. A no-op when
# nothing was recorded, so it is safe as a reset hook.
restore_env() {
  local deploy=$1 var=$2
  local key="${_ENV_PREFIX}${var}" rec container orig entry
  rec="$(_k get deploy "$deploy" -o jsonpath="{.metadata.annotations.${key}}")"
  if [ -z "$rec" ]; then
    echo "${deploy}: ${var} has no recorded original, nothing to restore"
    return 0
  fi
  container="${rec%%|*}"
  orig="${rec#*|}"
  if [ "$orig" = "__absent__" ]; then
    # The variable did not exist before the fault: remove it.
    entry="{\"name\":\"${var}\",\"\$patch\":\"delete\"}"
  elif [[ "$orig" == *'"valueFrom"'* ]]; then
    # Null the literal the fault set, so the valueFrom reference takes effect.
    entry="${orig%\}},\"value\":null}"
  else
    entry="${orig%\}},\"valueFrom\":null}"
  fi
  _k patch deploy "$deploy" --type=strategic -p \
    "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"${container}\",\"env\":[${entry}]}]}}}}" >/dev/null
  _k rollout status "deploy/${deploy}" --timeout=5m >/dev/null
  _k annotate deploy "$deploy" "${key}-" >/dev/null
  echo "${deploy}: ${var} restored"
}

# restore_all_env — restore every recorded env mutation on every Deployment in
# the namespace, whichever scenario made it.
restore_all_env() {
  local line deploy key
  while IFS=$'\t' read -r deploy line; do
    [ -n "$deploy" ] || continue
    for key in $(grep -o "\"${_ENV_PREFIX}[A-Za-z0-9_]*\"" <<<"$line" | tr -d '"'); do
      restore_env "$deploy" "${key#"${_ENV_PREFIX}"}"
    done
  done < <(_k get deploy -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations}{"\n"}{end}')
}
