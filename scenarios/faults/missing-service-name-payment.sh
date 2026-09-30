#!/usr/bin/env bash
# Fault for missing-service-name-payment: roll payment out with an empty
# OTEL_SERVICE_NAME, so its SDK falls back to the default resource name.
#
# otel-demo sets OTEL_SERVICE_NAME through a downward-API fieldRef. An empty
# literal overrides it, and the Node SDK then reports
# service.name="unknown_service:/nodejs/bin/node" (verified live: the job label
# in Mimir becomes that string). payment keeps working; its telemetry simply
# stops being attributable to payment.
#
# Run by `argus bench run --inject=auto`; restored by restore-all-mutations.sh.
set -euo pipefail
# shellcheck source=lib/env-fault.sh
source "$(dirname "$0")/lib/env-fault.sh"

set_env payment payment OTEL_SERVICE_NAME ""
echo "payment rolled out with an empty OTEL_SERVICE_NAME"
