#!/usr/bin/env bash
# Fault for cloudops-missing-service-name-payment: redeploy payment with an
# empty OTEL_SERVICE_NAME.
#
# Nothing breaks. Orders still complete; payment's telemetry keeps arriving,
# but the OTel Node SDK names it unknown_service:<process>, so payment seems
# to vanish while an unnamed service appears between the gateway and ledger.
#
# CloudOps sets OTEL_SERVICE_NAME as a literal; env-fault.sh records it and
# restore puts it back. Run by `argus bench run --inject=auto`; restored by
# restore-all-mutations.sh.
set -euo pipefail
# shellcheck source=lib/env-fault.sh
source "$(dirname "$0")/lib/env-fault.sh"

set_env payment payment OTEL_SERVICE_NAME ""
echo "payment rolled out with an empty OTEL_SERVICE_NAME"
