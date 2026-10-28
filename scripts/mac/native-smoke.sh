#!/bin/bash
# native-smoke.sh — real Tart + tart-hostctl smoke lane for one macOS host.
#
# Drives the host-control-plane verbs (create/get/list/delete/status) against a
# REAL Tart VM, then tears everything down. This is the native hardware lane;
# the Linux stub lane is test/integration/fake-host/.
#
# SAFETY: only ever touches VMs whose name starts with `garmtest-`. Any other
# VM in `tart list` is left strictly alone (and reported, not deleted).
#
# Usage (run ON the Mac):
#   scripts/mac/native-smoke.sh --run       # pull image, drive verbs, assert
#   scripts/mac/native-smoke.sh --cleanup   # idempotent teardown (safe twice)
#   scripts/mac/native-smoke.sh --help
#
# Env overrides:
#   TART_BIN                    tart binary          (default $HOME/bin/tart)
#   HOSTCTL_BIN                 tart-hostctl binary  (default $HOME/bin/tart-hostctl)
#   TART_FLEET_ROOT             fleet dir            (default $HOME/tart-fleet-test)
#   TART_FLEET_SLOTS_PER_HOST   slot cap             (default 1)
#   TART_FLEET_CONTROLLER_ID    controller label     (default native-smoke)
#   NATIVE_SMOKE_IMAGE          image to pull        (default ghcr.io/cirruslabs/ubuntu:latest)
#
# What this lane leaves installed (reversal):
#   tart and ~/bin/tart-hostctl stay installed for later cards (T4/T7).
#   Remove tart:     rm -rf ~/bin/tart ~/bin/tart.app
#   Remove hostctl:  rm -f  ~/bin/tart-hostctl
#
# Known defect (surfaced by this lane, fed back to T2): tart 2.x removed the
# `--label` flag, so the label-based adoption/quarantine distinction in
# tart-hostctl cannot identify this controller's own VMs after a crash on real
# hardware. An unrecorded VM is therefore quarantined (unlabelled), not adopted.
set -euo pipefail

TART_BIN="${TART_BIN:-$HOME/bin/tart}"
HOSTCTL_BIN="${HOSTCTL_BIN:-$HOME/bin/tart-hostctl}"
FLEET_ROOT="${TART_FLEET_ROOT:-$HOME/tart-fleet-test}"
SLOTS="${TART_FLEET_SLOTS_PER_HOST:-1}"
CONTROLLER="${TART_FLEET_CONTROLLER_ID:-native-smoke}"
IMAGE="${NATIVE_SMOKE_IMAGE:-ghcr.io/cirruslabs/ubuntu:latest}"

# VM the lane creates/tears down. Only names with this prefix are ever touched.
VM_PREFIX="garmtest-"
MAIN_VM="${VM_PREFIX}1"
SLOT_VM="${VM_PREFIX}2"      # the create that must be refused (slot cap)
ORPHAN_VM="${VM_PREFIX}3"    # the manually-cloned, unrecorded VM (reconcile)

export TART_FLEET_ROOT="$FLEET_ROOT"
export TART_FLEET_SLOTS_PER_HOST="$SLOTS"
export TART_FLEET_TART_BIN="$TART_BIN"
export TART_FLEET_CONTROLLER_ID="$CONTROLLER"

PASS=0
FAIL=0

ok()  { PASS=$((PASS + 1)); printf 'PASS: %s\n' "$*"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL: %s\n' "$*"; }

die() { printf 'FATAL: %s\n' "$*" >&2; exit 2; }

# hostctl <verb> ... -> runs hostctl, capturing stdout/stderr and exit code.
hostctl() {
  "$HOSTCTL_BIN" "$@"
}

# garmtest_vms -> newline-separated names of garmtest-* VMs currently in tart.
garmtest_vms() {
  "$TART_BIN" list --format json 2>/dev/null \
    | jq -r '.[].name' 2>/dev/null \
    | grep "^${VM_PREFIX}" || true
}

# foreign_vms -> newline-separated names of non-garmtest-* VMs in tart.
foreign_vms() {
  "$TART_BIN" list --format json 2>/dev/null \
    | jq -r '.[].name' 2>/dev/null \
    | grep -v "^${VM_PREFIX}" || true
}

usage() {
  cat <<EOF
native-smoke.sh — real Tart + tart-hostctl smoke lane.

Usage:
  $0 --run        pull the image, drive create/get/list/delete, assert, teardown
  $0 --cleanup    idempotent teardown of every garmtest-* VM + fleet dir + image cache
  $0 --help       this message

Reversal (what stays installed):
  rm -rf ~/bin/tart ~/bin/tart.app      # remove tart
  rm -f  ~/bin/tart-hostctl             # remove tart-hostctl
EOF
}

# ensure_prereqs checks tart/hostctl/jq are present and the image is local.
ensure_prereqs() {
  [ -x "$TART_BIN" ] || die "tart not found at $TART_BIN (set TART_BIN)"
  [ -x "$HOSTCTL_BIN" ] || die "tart-hostctl not found at $HOSTCTL_BIN (set HOSTCTL_BIN)"
  command -v jq >/dev/null 2>&1 || die "jq is required (not on PATH)"
}

# pull_image makes the smoke image local (idempotent: cached pulls are cheap).
pull_image() {
  printf '%s\n' "pull: $IMAGE"
  "$TART_BIN" pull "$IMAGE"
}

# run_smoke drives the verbs and asserts the contract.
run_smoke() {
  ensure_prereqs
  pull_image

  # --- 1. create: clone, boot headless, wait for the guest (tart exec). ---
  printf '%s\n' "create $MAIN_VM"
  create_out=$(hostctl create --name "$MAIN_VM" --image "$IMAGE" \
    --controller "$CONTROLLER" --cpu 4 --mem 4096 --disk 20 2>&1) \
    || { bad "create $MAIN_VM failed: $create_out"; }
  state=$(printf '%s' "$create_out" | jq -r '.state // empty')
  in_tart=$(printf '%s' "$create_out" | jq -r '.in_tart // empty')
  ip=$(printf '%s' "$create_out" | jq -r '.ip // empty')
  [ "$state" = "ready" ] && ok "create returns ready" || bad "create state=$state (want ready; out=$create_out)"
  [ "$in_tart" = "true" ] && ok "create reports in_tart=true" || bad "create in_tart=$in_tart"
  [ -n "$ip" ] && ok "create reports an IP ($ip)" || bad "create reported no IP"

  # --- 2. get ---
  get_out=$(hostctl get --name "$MAIN_VM" 2>&1) || bad "get $MAIN_VM failed: $get_out"
  [ "$(printf '%s' "$get_out" | jq -r '.name // empty')" = "$MAIN_VM" ] \
    && ok "get returns $MAIN_VM" || bad "get did not return $MAIN_VM: $get_out"

  # --- 3. list ---
  list_out=$(hostctl list 2>&1) || bad "list failed: $list_out"
  printf '%s' "$list_out" | jq -e --arg n "$MAIN_VM" '.[] | select(.name==$n)' >/dev/null 2>&1 \
    && ok "list contains $MAIN_VM" || bad "list missing $MAIN_VM: $list_out"

  # --- 4. slot cap: a second create beyond slots_per_host is refused. ---
  printf '%s\n' "create $SLOT_VM (expect capacity refusal)"
  slot_out=$(hostctl create --name "$SLOT_VM" --image "$IMAGE" \
    --controller "$CONTROLLER" --cpu 4 --mem 4096 --disk 20 2>&1) && slot_rc=0 || slot_rc=$?
  if [ "$slot_rc" -ne 0 ] && printf '%s' "$slot_out" | grep -q '"fault":"capacity"'; then
    ok "slot cap refuses second create (fault=capacity)"
  else
    bad "slot cap did not refuse second create (rc=$slot_rc out=$slot_out)"
  fi

  # --- 5. delete: proves stop+delete+release. ---
  hostctl delete --name "$MAIN_VM" >/dev/null 2>&1 && ok "delete $MAIN_VM exits 0" || bad "delete $MAIN_VM failed"
  if garmtest_vms | grep -qx "$MAIN_VM"; then
    bad "delete $MAIN_VM left it in tart list"
  else
    ok "delete $MAIN_VM released it from tart list"
  fi

  # --- 6. idempotent delete: deleting a missing VM is a no-op success. ---
  hostctl delete --name "$MAIN_VM" >/dev/null 2>&1 && ok "second delete exits 0 (idempotent)" \
    || bad "second delete of a missing VM did not exit 0"

  # --- 7. reconcile: an unrecorded VM is adopted/quarantined, not orphaned. ---
  # On tart 2.x (labels removed) an unrecorded VM is quarantined, not adopted;
  # see the "Known defect" note in the header.
  printf '%s\n' "reconcile: manual clone $ORPHAN_VM then status"
  "$TART_BIN" clone "$IMAGE" "$ORPHAN_VM" >/dev/null 2>&1 \
    || { bad "manual clone $ORPHAN_VM failed"; }
  status_out=$(hostctl status 2>&1) || bad "status failed: $status_out"
  quarantine=$(printf '%s' "$status_out" | jq -r '.capacity.quarantined // 0')
  [ "$quarantine" -ge 1 ] && ok "reconcile quarantines unrecorded VM (quarantined=$quarantine)" \
    || bad "reconcile did not quarantine $ORPHAN_VM: $status_out"
  hostctl delete --name "$ORPHAN_VM" >/dev/null 2>&1 && ok "delete orphan exits 0" \
    || bad "delete orphan $ORPHAN_VM failed"

  # --- 8. teardown sanity: no garmtest-* VM left behind. ---
  if [ -z "$(garmtest_vms)" ]; then
    ok "no garmtest-* VM remains after run"
  else
    bad "garmtest-* VMs remain after run: $(garmtest_vms | tr '\n' ' ')"
  fi

  echo
  echo "native-smoke --run: $PASS passed, $FAIL failed"
  [ "$FAIL" -eq 0 ]
}

# cleanup tears down everything this lane (or a crashed run) left behind.
cleanup() {
  ensure_prereqs

  printf '%s\n' "cleanup: fleet=$FLEET_ROOT image cache=$HOME/.tart/cache/OCIs/github.com/cirruslabs"

  # 1. Stop and delete every garmtest-* VM; leave foreign VMs strictly alone.
  local vm
  for vm in $(garmtest_vms); do
    printf '%s\n' "cleanup: stop+delete $vm"
    "$TART_BIN" stop "$vm" >/dev/null 2>&1 || true
    "$TART_BIN" delete "$vm" >/dev/null 2>&1 || true
  done

  # 2. Report (and never delete) any foreign VM present.
  local foreign
  foreign=$(foreign_vms)
  if [ -n "$foreign" ]; then
    printf '%s\n' "cleanup: leaving foreign VM(s) alone: $(printf '%s' "$foreign" | tr '\n' ' ')"
  fi

  # 3. Remove the fleet registry and any smoke scratch dirs.
  if [ -d "$FLEET_ROOT" ]; then
    rm -rf "$FLEET_ROOT"
    printf '%s\n' "cleanup: removed $FLEET_ROOT"
  fi
  for d in "$HOME"/tart-smoke-*; do
    [ -e "$d" ] || continue
    rm -rf "$d"
    printf '%s\n' "cleanup: removed scratch $d"
  done

  # 4. Remove the pulled image cache (re-downloadable; keeps disk back at baseline).
  if [ -d "$HOME/.tart/cache/OCIs/github.com/cirruslabs" ]; then
    rm -rf "$HOME/.tart/cache/OCIs/github.com/cirruslabs"
    printf '%s\n' "cleanup: removed image cache $HOME/.tart/cache/OCIs/github.com/cirruslabs"
  fi

  # 4b. Remove any partial pull temp data left by an interrupted pull.
  if [ -d "$HOME/.tart/tmp" ]; then
    rm -rf "$HOME/.tart/tmp"
    printf '%s\n' "cleanup: removed partial pull temp $HOME/.tart/tmp"
  fi

  # 5. Report the post-cleanup state.
  printf '%s\n' "cleanup: tart list after:"
  "$TART_BIN" list 2>&1 || true
  printf '%s\n' "cleanup: du -sh ~/.tart:"
  du -sh "$HOME/.tart" 2>/dev/null || true
  printf '%s\n' "cleanup: df -k /:"
  df -k / | tail -1

  # 6. Idempotency: nothing garmtest-* should remain.
  if [ -z "$(garmtest_vms)" ]; then
    printf '%s\n' "cleanup: complete (no garmtest-* VMs)"
  else
    printf '%s\n' "cleanup: WARNING garmtest-* VMs remain: $(garmtest_vms | tr '\n' ' ')" >&2
    return 1
  fi
}

case "${1:-}" in
  --run)      run_smoke ;;
  --cleanup)  cleanup ;;
  --help|-h)  usage ;;
  *)          usage; exit 2 ;;
esac
