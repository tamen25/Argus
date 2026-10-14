#!/usr/bin/env python3
"""Says whether a bench run report is the matrix cell asked for.

Used by bench-matrix.sh to decide whether a cell is done. Prints one word:
  done     valid JSON for the same condition, repeats and budget
  other    valid JSON made with other settings
  invalid  not a readable JSON report

Usage: report-matches.py <report.json> <condition> <repeats> <max-tool-calls> <max-tokens>
"""
import json
import sys


def main(path, condition, repeats, max_calls, max_tokens):
    try:
        with open(path, encoding="utf-8") as f:
            report = json.load(f)
    except (OSError, ValueError):
        return "invalid"
    if not isinstance(report, dict):
        return "invalid"
    # The budget's keys are the Go field names: agent.Budget has no JSON
    # tags, and published reports already use these keys.
    budget = report.get("budget") or {}
    same = (
        report.get("condition") == condition
        and len(report.get("runs") or []) == int(repeats)
        and budget.get("MaxToolCalls") == int(max_calls)
        and budget.get("MaxTokens") == int(max_tokens)
    )
    return "done" if same else "other"


if __name__ == "__main__":
    if len(sys.argv) != 6:
        sys.exit(__doc__)
    print(main(*sys.argv[1:]))
