#!/usr/bin/env bash
# Fault for deploy-regression-cart: roll cart out with a bad config value — its
# cache address points at a port nothing listens on.
#
# This is a bad rollout, not a bad image. With one replica and a RollingUpdate,
# an unpullable image never replaces the healthy pod, so nothing breaks; and
# forcing Recreate would only reproduce oomkill-checkout's outage signal. A
# config regression gives the shape that matters: the rollout SUCCEEDS (the pod
# starts and passes readiness), then every cart operation fails (verified live:
# frontend 5xx 0.016-0.065/s, clearing within ~200s of restore).
#
# Run by `argus bench run --inject=auto`; restored by restore-all-mutations.sh.
set -euo pipefail
# shellcheck source=lib/env-fault.sh
source "$(dirname "$0")/lib/env-fault.sh"

set_env cart cart VALKEY_ADDR valkey-cart:6380
echo "cart rolled out with VALKEY_ADDR=valkey-cart:6380"
