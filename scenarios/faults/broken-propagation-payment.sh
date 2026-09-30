#!/usr/bin/env bash
# Fault for broken-trace-propagation-payment: roll payment out with
# OTEL_PROPAGATORS=none, so it neither extracts nor injects trace context.
#
# payment keeps serving every request, and still emits spans — but each server
# span starts a new trace instead of joining checkout's. The checkout -> payment
# edge disappears from the service graph while payment's own request rate is
# unchanged (verified live: edge rate 0 while payment served ~0.08/s; at
# baseline the two are equal).
#
# Run by `argus bench run --inject=auto`; restored by restore-all-mutations.sh.
set -euo pipefail
# shellcheck source=lib/env-fault.sh
source "$(dirname "$0")/lib/env-fault.sh"

set_env payment payment OTEL_PROPAGATORS none
echo "payment rolled out with OTEL_PROPAGATORS=none"
