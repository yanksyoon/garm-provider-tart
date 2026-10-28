#!/usr/bin/env bash
# shellcheck shell=bash
# Shared helpers for the bootstrap stub binaries under scripts/mac/testdata/bin.
#
# Each stub logs its argv to $STUB_LOG and records "already configured" markers
# under $BOOTSTRAP_STUB_STATE, so test-bootstrap.sh can drive host-bootstrap.sh
# through the fresh and already-configured paths and assert idempotency. The
# markers are files: `mark <key>` creates one, `has <key>` tests one, `get`/`put`
# store/read a value.
#
# A stub binary sources this file and then behaves like its macOS counterpart,
# but against the in-memory marker store instead of the real system.

STUB_LOG="${STUB_LOG:-/dev/null}"
STUB_STATE="${BOOTSTRAP_STUB_STATE:-/tmp/garm-bootstrap-stub-state}"

stub_log() { printf '%s %s\n' "$(basename "$0")" "$*" >>"$STUB_LOG"; }
mark() { mkdir -p "$STUB_STATE"; : >"$STUB_STATE/$1"; }
unmark() { rm -f "$STUB_STATE/$1"; }
has() { [ -e "$STUB_STATE/$1" ]; }
get() { cat "$STUB_STATE/$1" 2>/dev/null || true; }
put() { mkdir -p "$STUB_STATE"; printf '%s' "$2" >"$STUB_STATE/$1"; }
