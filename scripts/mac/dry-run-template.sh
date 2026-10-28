#!/usr/bin/env bash
# Dry-run the rendered macOS runner-install template against a fake curl and
# fixture credential files, so the JIT/non-JIT flow can be exercised on Linux
# without a Mac, Tart, or a live GARM controller.
#
# The template is rendered by render-template.sh, then executed with:
#   * a fake curl that serves fixture credentials by URL path and records the
#     status callbacks;
#   * a fake security(1) that reports the login keychain as unlocked;
#   * a cached runner marker (run.sh) so the download/tar step is skipped;
#   * a fake darwin.svc.sh.template so ensure_svc_script can generate svc.sh.
#
# The rendered script is run TWICE on the same RUN_HOME to prove requirement
# #8 (idempotency): the second run must detect the already-configured state,
# skip registration/install, and only (re)start the service.
#
# Usage:
#   scripts/mac/dry-run-template.sh            # JIT (default)
#   scripts/mac/dry-run-template.sh --nojit    # non-JIT
#
# Exits 0 only when both runs post the "runner started" idle callback and the
# second run does not re-register (config.sh) or re-install (svc.sh install).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
scripts_dir="$repo_root/scripts/mac"

mode="jit"
if [ "${1:-}" = "--nojit" ]; then
  mode="nojit"
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

runhome="$workdir/home/actions-runner"
mkdir -p "$runhome/bin" "$workdir/bin"

# Cached runner marker: the golden image ships run.sh, so the download (and its
# tar step) is skipped. This is the cache-friendly branch.
: > "$runhome/run.sh"

# The darwin service template that ensure_svc_script turns into ./svc.sh.
cp "$scripts_dir/testdata/fixtures/darwin.svc.sh.template" "$runhome/bin/darwin.svc.sh.template"

# security(1) stub: report the login keychain as unlocked.
cat > "$workdir/bin/security" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$workdir/bin/security"

# The non-JIT path runs config.sh (which ships in the runner root), and is
# expected to generate svc.sh and .runner itself. The stub records each
# invocation in CONFIG_LOG so the dry run can prove the second run does NOT
# re-register.
if [ "$mode" = "nojit" ]; then
  cat > "$runhome/config.sh" <<'EOF'
#!/usr/bin/env bash
# config.sh stub: pretend to register and produce .runner + svc.sh. Mirrors the
# real guard: refuse to configure when .runner already exists.
echo config >> "${CONFIG_LOG:-/dev/null}"
if [ -f .runner ]; then
  echo "Cannot configure the runner because it is already configured." >&2
  exit 1
fi
cat > .runner <<'RUNNER'
{"agentId":4242,"agentName":"garm-dryrun"}
RUNNER
cat > svc.sh <<'SVC'
#!/usr/bin/env bash
echo "svc.sh $1" >> "${SVC_LOG:-/dev/null}"
exit 0
SVC
chmod +x svc.sh
exit 0
EOF
  chmod +x "$runhome/config.sh"
fi

ln -s "$scripts_dir/testdata/fake-curl" "$workdir/bin/curl"

rendered="$workdir/macos-runner.sh"
if [ "$mode" = "nojit" ]; then
  "$scripts_dir/render-template.sh" --nojit > "$rendered"
else
  "$scripts_dir/render-template.sh" > "$rendered"
fi

# Run the rendered script twice with the fake tools first on PATH. RUN_HOME is
# derived from $HOME, so point HOME at the workdir. The shared SVC_LOG/CONFIG_LOG
# let us count install/start/config invocations across both runs.
run_script() {
  HOME="$workdir/home" \
    CALLBACK_URL="http://garm.invalid/api/v1/callbacks/status/" \
    METADATA_URL="http://garm.invalid/api/v1/metadata/" \
    BEARER_TOKEN="dry-run-test-token" \
    PATH="$workdir/bin:$PATH" \
    FAKE_CURL_FIXTURES="$scripts_dir/testdata/fixtures" \
    FAKE_CURL_CALLBACK_LOG="$workdir/callbacks.log" \
    SVC_LOG="$workdir/svc.log" \
    CONFIG_LOG="$workdir/config.log" \
    bash "$rendered"
}

# First run performs the fresh install; the second run on the same RUN_HOME
# proves the idempotency branch.
run_script
run_script

# Assert the terminal callback reported the runner as idle/started (once per
# run, so at least twice total).
if [ "$(grep -c '"status":"idle"' "$workdir/callbacks.log" || true)" -lt 2 ]; then
  echo "dry-run: expected an idle status callback from both runs" >&2
  cat "$workdir/callbacks.log" >&2 || true
  exit 1
fi
if [ "$(grep -c '"runner started"' "$workdir/callbacks.log" || true)" -lt 2 ]; then
  echo "dry-run: expected a 'runner started' callback from both runs" >&2
  cat "$workdir/callbacks.log" >&2 || true
  exit 1
fi

# Idempotency: svc.sh install must run exactly once (the second run must not
# re-install the launchd job), while svc.sh start runs on both runs.
install_count="$(grep -c 'svc.sh install' "$workdir/svc.log" || true)"
start_count="$(grep -c 'svc.sh start' "$workdir/svc.log" || true)"
if [ "$install_count" -ne 1 ]; then
  echo "dry-run: expected svc.sh install exactly once, got $install_count" >&2
  cat "$workdir/svc.log" >&2 || true
  exit 1
fi
if [ "$start_count" -ne 2 ]; then
  echo "dry-run: expected svc.sh start exactly twice, got $start_count" >&2
  cat "$workdir/svc.log" >&2 || true
  exit 1
fi

# Non-JIT only: config.sh (registration) must run exactly once — the second
# run must not re-register.
if [ "$mode" = "nojit" ]; then
  config_count="$(grep -c 'config' "$workdir/config.log" || true)"
  if [ "$config_count" -ne 1 ]; then
    echo "dry-run: expected config.sh exactly once, got $config_count" >&2
    cat "$workdir/config.log" >&2 || true
    exit 1
  fi
  if ! grep -q '"agent_id":4242' "$workdir/callbacks.log"; then
    echo "dry-run: missing agent_id in idle callback" >&2
    exit 1
  fi
fi

echo "dry-run ($mode): ok — two runs, single install, idempotent second run"
