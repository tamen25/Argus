#!/usr/bin/env bash
# Enforce the coverage bar from CLAUDE.md ("Quality bar") on the deterministic
# core: the packages whose output is a score, a cost, or a verdict, and so must
# be the most thoroughly tested code in the repo.
#
# Reads `go test -cover` output rather than running the tests itself, so CI runs
# the suite once and gates on the result. Locally:
#
#   (cd engine && go test -cover ./... > /tmp/cover.out); scripts/coverage-gate.sh /tmp/cover.out
#
# Exits non-zero if any gated package is below the threshold OR reports no
# coverage at all: a package that silently drops out of the report (renamed,
# tests deleted, build tag) must not count as passing.
set -euo pipefail

out="${1:?usage: coverage-gate.sh <file holding 'go test -cover' output>}"
threshold="${COVERAGE_THRESHOLD:-70}"
module="github.com/tamen25/Argus/engine"

gated=(
  internal/rules
  internal/cost
  internal/backtest
  internal/bench/scoring
)

fail=0
for pkg in "${gated[@]}"; do
  # Match the package path exactly: internal/rules must not also match
  # internal/rules/builtin. `go test` puts the import path in field 2 for both
  # fresh and (cached) results.
  pct="$(awk -v p="$module/$pkg" '$2 == p { for (i = 1; i <= NF; i++) if ($i ~ /%$/) { sub(/%/, "", $i); print $i; exit } }' "$out")"

  if [[ -z "$pct" ]]; then
    echo "::error::no coverage reported for $pkg — was it renamed, or did its tests stop running?"
    fail=1
    continue
  fi

  if awk -v a="$pct" -v t="$threshold" 'BEGIN { exit !(a + 0 < t + 0) }'; then
    echo "::error::$pkg coverage ${pct}% is below the ${threshold}% bar"
    fail=1
  else
    printf 'ok    %-26s %6s%%\n' "$pkg" "$pct"
  fi
done

exit "$fail"
