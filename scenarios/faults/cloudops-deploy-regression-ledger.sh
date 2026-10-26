#!/usr/bin/env bash
# Fault for cloudops-deploy-regression-ledger: roll ledger out with an
# internal HMAC key that no longer matches the one payment signs with.
#
# The rollout succeeds: ledger starts, passes readiness and reports. Then it
# refuses every signed post, payment answers each authorization with an
# error, and the gateway turns that into 402 payment_failed. The symptom
# names payment; the change is ledger's.
#
# CloudOps sets INTERNAL_HMAC_SECRET from the mesh-internal-hmac Secret;
# env-fault.sh records that valueFrom reference and restore puts it back.
# Run by `argus bench run --inject=auto`; restored by restore-all-mutations.sh.
set -euo pipefail
# shellcheck source=lib/env-fault.sh
source "$(dirname "$0")/lib/env-fault.sh"

set_env ledger ledger INTERNAL_HMAC_SECRET argus-bench-rotated-key
echo "ledger rolled out with a rotated INTERNAL_HMAC_SECRET"
