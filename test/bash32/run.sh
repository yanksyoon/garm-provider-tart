#!/bin/sh
# bash32 test driver. Runs INSIDE the container: renders the macOS
# runner-install template exactly as GARM does, syntax-checks it with
# `bash -n`, then executes it against fake curl/launchctl/security stubs and
# asserts the JIT/non-JIT branches and the failure paths.
#
# The first thing it does is refuse to run unless `bash` is exactly 3.2.x, so
# the lane can never silently degrade to bash 5.
set -eu

RENDER_BIN=${RENDER_BIN:-/usr/local/bin/render}
TEMPLATE=${TEMPLATE:-/opt/bash32/template/macos-runner.sh}
STUB_DIR=${STUB_DIR:-/opt/bash32/stubs}
FIXTURES=${FIXTURES:-/opt/bash32/fixtures/metadata}
RUNNER_DIR=${RUNNER_DIR:-/opt/bash32/runner}
BASH_BIN=${BASH_BIN:-bash}

RUN_HOME=/Users/runner/actions-runner

PASS=0
FAIL=0

ok()  { PASS=$((PASS + 1)); printf 'PASS: %s\n' "$*"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL: %s\n' "$*"; }

assert_contains() { # name needle haystack
	if printf '%s' "$3" | grep -q -F "$2"; then
		ok "$1"
	else
		bad "$1 (expected substring [$2], got: [$3])"
	fi
}

assert_not_contains() { # name needle haystack
	if printf '%s' "$3" | grep -q -F "$2"; then
		bad "$1 (did not expect substring [$2] in: [$3])"
	else
		ok "$1"
	fi
}

# --- 0. bash version guard: exactly 3.2.x, no silent degradation to bash 5 ---
VLINE=$("$BASH_BIN" --version 2>&1 | sed -n '1p')
case "$VLINE" in
	*"version 3.2."*) ok "bash version guard ($VLINE)" ;;
	*) echo "REFUSE: bash is not exactly 3.2.x ($VLINE)" >&2; exit 1 ;;
esac

# Ensure the runner user/group the template chowns to exist.
if ! getent group staff >/dev/null 2>&1; then groupadd staff; fi
if ! getent passwd runner >/dev/null 2>&1; then useradd -M -g staff runner; fi

WORKDIR=$(mktemp -d /tmp/bash32.XXXXXX)
trap 'rm -rf "$WORKDIR"' EXIT

# Common stub environment.
export CURL_STUB_METADATA_URL=http://metadata.invalid
export CURL_STUB_CALLBACK_URL=http://callback.invalid
export CURL_STUB_DOWNLOAD_URL=https://downloads.example.invalid/runner.tar.gz
export PATH="$STUB_DIR:$PATH"

# render <jit|non-jit> <outfile>
render() {
	if [ "$1" = "jit" ]; then
		"$RENDER_BIN" -template "$TEMPLATE" -jit > "$2"
	else
		"$RENDER_BIN" -template "$TEMPLATE" -non-jit > "$2"
	fi
}

# reset_runhome: build a fresh fake runner dir (cached runner present by
# default), so every scenario starts from a clean state.
reset_runhome() {
	rm -rf "$RUN_HOME"
	mkdir -p "$RUN_HOME/bin"
	cp "$RUNNER_DIR/run.sh" "$RUN_HOME/run.sh"
	cp "$RUNNER_DIR/svc.sh" "$RUN_HOME/svc.sh"
	cp "$RUNNER_DIR/config.sh" "$RUN_HOME/config.sh"
	chmod +x "$RUN_HOME/run.sh" "$RUN_HOME/svc.sh" "$RUN_HOME/config.sh"
}

# new_scenario <name>: create an isolated dir and point all stub logs at it.
new_scenario() {
	SCENARIO=$(mktemp -d "$WORKDIR/$1.XXXXXX")
	export CURL_STUB_CALLBACK_LOG="$SCENARIO/callback.log"
	export CURL_STUB_LOG="$SCENARIO/curl.log"
	export SECURITY_STUB_LOG="$SCENARIO/security.log"
	export LAUNCHCTL_STUB_LOG="$SCENARIO/launchctl.log"
	export SVC_SH_LOG="$SCENARIO/svc.log"
	export CONFIG_SH_LOG="$SCENARIO/config.log"
	export SECURITY_STUB_LOCKED=0
	unset CURL_STUB_META_STATUS
	unset CURL_STUB_RUNNER_TARBALL
	# Per-scenario fixture copy so tests can remove/override individual files.
	mkdir -p "$SCENARIO/fixtures"
	cp -R "$FIXTURES"/. "$SCENARIO/fixtures"/
	export CURL_STUB_FIXTURES="$SCENARIO/fixtures"
}

# run_script <script> -> sets RC
run_script() {
	set +e
	"$BASH_BIN" "$1" >"$SCENARIO/out.log" 2>&1
	RC=$?
	set -e
}

# callback_log -> stdin-safe read of the callback log
callback_log() { cat "$CURL_STUB_CALLBACK_LOG" 2>/dev/null || true; }

# --- 1. render both branches and syntax-check under bash 3.2 ---
render jit "$WORKDIR/jit.sh"
render non-jit "$WORKDIR/nonjit.sh"

if "$BASH_BIN" -n "$WORKDIR/jit.sh"; then ok "bash -n JIT rendered script"; else bad "bash -n JIT rendered script"; fi
if "$BASH_BIN" -n "$WORKDIR/nonjit.sh"; then ok "bash -n non-JIT rendered script"; else bad "bash -n non-JIT rendered script"; fi

# --- 2. JIT happy path (cached runner) ---
new_scenario jit-happy
reset_runhome
render jit "$SCENARIO/jit.sh"
run_script "$SCENARIO/jit.sh"
if [ "$RC" -eq 0 ]; then ok "JIT happy path exits 0"; else bad "JIT happy path exits $RC (out: $(cat "$SCENARIO/out.log"))"; fi
assert_contains "JIT happy path posts idle" "idle" "$(callback_log)"
assert_not_contains "JIT happy path posts no failed" "failed" "$(callback_log)"
assert_contains "JIT installs the service" "svc.sh install" "$(cat "$SVC_SH_LOG" 2>/dev/null || true)"
assert_contains "JIT starts the service" "svc.sh start" "$(cat "$SVC_SH_LOG" 2>/dev/null || true)"

# --- 3. non-JIT happy path (cached runner) ---
new_scenario nonjit-happy
reset_runhome
render non-jit "$SCENARIO/nonjit.sh"
run_script "$SCENARIO/nonjit.sh"
if [ "$RC" -eq 0 ]; then ok "non-JIT happy path exits 0"; else bad "non-JIT happy path exits $RC (out: $(cat "$SCENARIO/out.log"))"; fi
assert_contains "non-JIT happy path posts idle" "idle" "$(callback_log)"
assert_contains "non-JIT reports agent id" "4242" "$(callback_log)"
assert_contains "non-JIT runs config.sh" "config.sh" "$(cat "$CONFIG_SH_LOG" 2>/dev/null || true)"
assert_not_contains "non-JIT happy path posts no failed" "failed" "$(callback_log)"

# --- 4. 401 from metadata -> fail cleanly ---
new_scenario meta-401
reset_runhome
render jit "$SCENARIO/jit.sh"
export CURL_STUB_META_STATUS=401
run_script "$SCENARIO/jit.sh"
if [ "$RC" -ne 0 ]; then ok "401 metadata exits non-zero ($RC)"; else bad "401 metadata exited 0"; fi
assert_contains "401 metadata posts failed" "failed" "$(callback_log)"
assert_contains "401 metadata message names the runner file" "get runner file" "$(callback_log)"
unset CURL_STUB_META_STATUS

# --- 5. missing credentials -> fail cleanly ---
new_scenario missing-credentials
reset_runhome
render jit "$SCENARIO/jit.sh"
rm -f "$CURL_STUB_FIXTURES/credentials/credentials"
run_script "$SCENARIO/jit.sh"
if [ "$RC" -ne 0 ]; then ok "missing credentials exits non-zero ($RC)"; else bad "missing credentials exited 0"; fi
assert_contains "missing credentials posts failed" "failed" "$(callback_log)"
assert_contains "missing credentials names the credentials file" "get credentials file" "$(callback_log)"

# --- 6. locked keychain -> fail cleanly ---
new_scenario locked-keychain
reset_runhome
render jit "$SCENARIO/jit.sh"
export SECURITY_STUB_LOCKED=1
run_script "$SCENARIO/jit.sh"
if [ "$RC" -ne 0 ]; then ok "locked keychain exits non-zero ($RC)"; else bad "locked keychain exited 0"; fi
assert_contains "locked keychain posts failed" "failed" "$(callback_log)"
assert_contains "locked keychain names the keychain" "keychain" "$(callback_log)"
unset SECURITY_STUB_LOCKED

# --- 7. missing svc.sh -> fail cleanly ---
new_scenario missing-svc
reset_runhome
rm -f "$RUN_HOME/svc.sh"
render non-jit "$SCENARIO/nonjit.sh"
run_script "$SCENARIO/nonjit.sh"
if [ "$RC" -ne 0 ]; then ok "missing svc.sh exits non-zero ($RC)"; else bad "missing svc.sh exited 0"; fi
assert_contains "missing svc.sh posts failed" "failed" "$(callback_log)"
assert_contains "missing svc.sh names the service install" "install service" "$(callback_log)"

# --- 8. download path (no cached runner) -> success ---
new_scenario download
rm -rf "$RUN_HOME"
tar -C "$RUNNER_DIR" -cf "$SCENARIO/runner.tar.gz" run.sh svc.sh config.sh
export CURL_STUB_RUNNER_TARBALL="$SCENARIO/runner.tar.gz"
render jit "$SCENARIO/jit.sh"
run_script "$SCENARIO/jit.sh"
if [ "$RC" -eq 0 ]; then ok "download path exits 0"; else bad "download path exits $RC (out: $(cat "$SCENARIO/out.log"))"; fi
assert_contains "download path posts idle" "idle" "$(callback_log)"

# --- 9. negative control: a bash-4-only construct must fail under bash 3.2 ---
new_scenario negative-control
reset_runhome
render jit "$SCENARIO/jit.sh"
sed '/set -o pipefail/a declare -A x' "$SCENARIO/jit.sh" > "$SCENARIO/neg.sh"
run_script "$SCENARIO/neg.sh"
if [ "$RC" -ne 0 ]; then
	ok "negative control: declare -A rejected under bash 3.2 (exit $RC)"
else
	bad "negative control: declare -A accepted — the lane may have degraded to bash 4+"
fi

# --- 10. idempotent second run leaves no re-registration (non-JIT) ---
new_scenario run-twice
reset_runhome
render non-jit "$SCENARIO/nonjit.sh"
run_script "$SCENARIO/nonjit.sh"
run_script "$SCENARIO/nonjit.sh"
if [ "$RC" -eq 0 ]; then ok "second run exits 0"; else bad "second run exits $RC (out: $(cat "$SCENARIO/out.log"))"; fi
CONFIG_COUNT=$(grep -c 'config.sh' "$CONFIG_SH_LOG" 2>/dev/null || echo 0)
assert_contains "second run does not re-register" "already configured" "$(callback_log)"
if [ "$CONFIG_COUNT" = "1" ]; then ok "config.sh runs exactly once across two runs"; else bad "config.sh ran $CONFIG_COUNT times (expected 1)"; fi

# --- summary ---
echo
echo "bash32: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
