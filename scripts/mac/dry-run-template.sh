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
# Usage:
#   scripts/mac/dry-run-template.sh            # JIT (default)
#   scripts/mac/dry-run-template.sh --nojit    # non-JIT
#
# Exits 0 only when the script succeeds and the "runner started" idle callback
# was posted.
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
# expected to generate svc.sh and .runner itself.
if [ "$mode" = "nojit" ]; then
  cat > "$runhome/config.sh" <<'EOF'
#!/usr/bin/env bash
# config.sh stub: pretend to register and produce .runner + svc.sh.
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

# Run the rendered script with the fake tools first on PATH. RUN_HOME is
# derived from $HOME, so point HOME at the workdir.
HOME="$workdir/home" \
  PATH="$workdir/bin:$PATH" \
  FAKE_CURL_FIXTURES="$scripts_dir/testdata/fixtures" \
  FAKE_CURL_CALLBACK_LOG="$workdir/callbacks.log" \
  SVC_LOG="$workdir/svc.log" \
  bash "$rendered"

# Assert the terminal callback reported the runner as idle/started.
if ! grep -q '"status":"idle"' "$workdir/callbacks.log"; then
  echo "dry-run: missing idle status callback" >&2
  cat "$workdir/callbacks.log" >&2 || true
  exit 1
fi
if ! grep -q '"runner started"' "$workdir/callbacks.log"; then
  echo "dry-run: missing 'runner started' callback" >&2
  cat "$workdir/callbacks.log" >&2 || true
  exit 1
fi

# Assert the service was installed and started with the substituted name.
for verb in install start; do
  if ! grep -q "svc.sh $verb" "$workdir/svc.log"; then
    echo "dry-run: missing svc.sh $verb" >&2
    cat "$workdir/svc.log" >&2 || true
    exit 1
  fi
done

if [ "$mode" = "nojit" ]; then
  if ! grep -q '"agent_id":4242' "$workdir/callbacks.log"; then
    echo "dry-run: missing agent_id in idle callback" >&2
    exit 1
  fi
fi

echo "dry-run ($mode): ok — 'runner started' idle callback posted"
