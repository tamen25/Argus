#!/usr/bin/env bash
# Reset/cleanup hook shared by every scenario that mutates an existing workload.
#
# Restores EVERY recorded argus mutation in the namespace — not only the one the
# current scenario makes — so a mutation leaked by a run that crashed before its
# cleanup is repaired before the next mutation scenario injects. This is the
# mutation counterpart of the injector's label sweep, which removes leaked fault
# OBJECTS; see docs/bench/authoring-scenarios.md rules 6 and 7. Idempotent: with
# nothing recorded, it changes nothing.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib/env-fault.sh
source "${here}/lib/env-fault.sh"

restore_all_env
# The OOMKill fault records its originals in its own annotations.
bash "${here}/restore-checkout-memory.sh"
