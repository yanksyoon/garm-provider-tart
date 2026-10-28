#!/usr/bin/env bash
# Host-side reaper for the Tart fleet. Run periodically (launchd/cron) as root.
#
# 1. `status` triggers Reconcile, which reaps quarantined VMs past their TTL,
#    drops records whose VM vanished, reclaims cloning/running records stranded
#    by a crashed create past the stale-create TTL, and adopts/quarantines
#    unrecorded VMs.
# 2. Complete `deleting` tombstones whose VM is already gone by re-invoking
#    `delete`, which proves release (absent from `tart list`) and frees the slot.
#
# Configure with:
#   TART_FLEET_ROOT  fleet directory        (default /var/db/tart-fleet)
#   TART_HOSTCTL     tart-hostctl binary    (default /usr/local/bin/tart-hostctl)
#   TART_SWEEP_LOG   log file               (default /var/log/tart-sweep.log)
set -euo pipefail

FLEET_ROOT="${TART_FLEET_ROOT:-/var/db/tart-fleet}"
HOSTCTL="${TART_HOSTCTL:-/usr/local/bin/tart-hostctl}"
LOG="${TART_SWEEP_LOG:-/var/log/tart-sweep.log}"
INSTANCES_DIR="$FLEET_ROOT/instances"

mkdir -p "$(dirname "$LOG")"

# Trigger reconcile so quarantined VMs past their TTL are reaped here.
"$HOSTCTL" status >/dev/null 2>>"$LOG"

# Complete tombstones: a `deleting` record whose VM is already gone frees the
# slot when `delete` confirms it is absent from `tart list`. The registry record
# is the host-owned truth, so reading it directly is safe and needs no jq.
if [ -d "$INSTANCES_DIR" ]; then
  for rec in "$INSTANCES_DIR"/*.json; do
    [ -e "$rec" ] || continue
    if grep -q '"state": "deleting"' "$rec"; then
      name="$(basename "$rec" .json)"
      "$HOSTCTL" delete --name "$name" >>"$LOG" 2>&1 || true
    fi
  done
fi
