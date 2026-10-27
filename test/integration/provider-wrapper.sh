#!/bin/sh
# Provider wrapper for the integration test: capture each GARM invocation's
# environment and stdin before delegating to the real provider binary. This is
# how the test driver observes what GARM actually sent (GARM_COMMAND, the
# BootstrapInstance JSON on stdin, GARM_POOL_ID, ...) without modifying the
# provider.
set -eu

# GARM runs external providers with a minimal environment (no PATH), so pin a
# full PATH before delegating; the provider shells out to `ssh` and the stub
# host binaries rely on it too.
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

logdir=/tmp/garm-provider-capture
mkdir -p "$logdir"

# Each provider invocation is its own process, so the PID is unique enough to
# name the stdin capture file without racing a concurrent ListInstances call.
body="$logdir/stdin-$$"
cat >"$body"

{
  printf 'CALL pid=%s GARM_COMMAND=%s GARM_POOL_ID=%s GARM_INSTANCE_ID=%s GARM_CONTROLLER_ID=%s stdin=%s\n' \
    "$$" \
    "${GARM_COMMAND:-}" \
    "${GARM_POOL_ID:-}" \
    "${GARM_INSTANCE_ID:-}" \
    "${GARM_CONTROLLER_ID:-}" \
    "$body"
} >>"$logdir/calls.log"

exec /opt/garm-tart/garm-provider-tart <"$body"
