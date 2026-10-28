#!/bin/bash
# GARM runner-install template for macOS (Tart guests). GARM renders this Go
# text/template and serves it at /install-script/; the POSIX wrapper GARM
# injects into extra_specs["runner_install_template"] fetches and runs it inside
# the guest. It therefore runs as a plain script, not a cloud-init payload.
#
# GARM's renderer (text/template, no FuncMap) performs no shell escaping, so
# this template never interpolates a context value into a shell-executed
# position. Every context value — RepoURL, DownloadURL, RunnerName,
# RunnerLabels, GitHubRunnerGroup, CallbackURL, MetadataURL, CallbackToken — is
# read from a quoted here-document into a shell variable and used only via
# double-quoted "$VAR" references. A quoted here-document delimiter suppresses
# all expansion, so $(), backticks, quotes, % and unicode in a value are
# literal data, not code.
#
# Constraints (macOS /bin/bash 3.2):
#   * no associative arrays, no mapfile, no `sed -i` without a suffix;
#   * the runner is supervised by launchd via ./svc.sh install|start (no
#     systemd unit handling — there is no systemd on macOS);
#   * the Linux-only dependency installer is never invoked.
set -euo pipefail
{{- if .EnableBootDebug }}
set -x
{{- end }}

# Read every template context value from a quoted here-document. A quoted
# delimiter suppresses all shell expansion inside the body, so a hostile value
# cannot break out of its quoting context or execute: it is captured as literal
# data, then used only via double-quoted "$VAR" references. The three secrets
# (CallbackURL, MetadataURL, CallbackToken) are rendered here rather than
# inherited from the environment: GARM's injected wrapper (linux_wrapper.tmpl)
# assigns CALLBACK_URL/METADATA_URL/BEARER_TOKEN without `export` and runs this
# install script as a child process, so those variables never reach it.
# These fields are single-line (a URL, a name, comma-separated labels, a group),
# so `read -r` takes the first line and any injected newline stays inside the
# discarded here-document. The only escape is a value line exactly equal to the
# delimiter below, which none of these fields can legitimately contain.
IFS= read -r CALLBACK_URL <<'GARM_MACOS_EOV' || true
{{ .CallbackURL }}
GARM_MACOS_EOV
IFS= read -r METADATA_URL <<'GARM_MACOS_EOV' || true
{{ .MetadataURL }}
GARM_MACOS_EOV
IFS= read -r BEARER_TOKEN <<'GARM_MACOS_EOV' || true
{{ .CallbackToken }}
GARM_MACOS_EOV
IFS= read -r REPO_URL <<'GARM_MACOS_EOV' || true
{{ .RepoURL }}
GARM_MACOS_EOV
IFS= read -r DOWNLOAD_URL <<'GARM_MACOS_EOV' || true
{{ .DownloadURL }}
GARM_MACOS_EOV
IFS= read -r RUNNER_NAME <<'GARM_MACOS_EOV' || true
{{ .RunnerName }}
GARM_MACOS_EOV
IFS= read -r RUNNER_LABELS <<'GARM_MACOS_EOV' || true
{{ .RunnerLabels }}
GARM_MACOS_EOV
IFS= read -r GITHUB_RUNNER_GROUP <<'GARM_MACOS_EOV' || true
{{ .GitHubRunnerGroup }}
GARM_MACOS_EOV

# Fail loudly before any use if a required value is empty. GARM always supplies
# these context values, so an empty one means a broken template or render;
# surfacing it here beats a confusing curl/config.sh failure later.
[ -n "$CALLBACK_URL" ] || { echo "garm-macos-runner: CALLBACK_URL is empty (missing .CallbackURL in the template context)" >&2; exit 1; }
[ -n "$METADATA_URL" ] || { echo "garm-macos-runner: METADATA_URL is empty (missing .MetadataURL in the template context)" >&2; exit 1; }
[ -n "$BEARER_TOKEN" ] || { echo "garm-macos-runner: BEARER_TOKEN is empty (missing .CallbackToken in the template context)" >&2; exit 1; }

# The actions-runner root lives under the runner user's home. The golden image
# bakes the runner binary here so the download step can be skipped.
RUN_HOME="${HOME}/actions-runner"

# Point CALLBACK_URL at the /status endpoint. GARM normally supplies a URL that
# already ends in /status/, but be defensive about a bare callback base.
case "$CALLBACK_URL" in
  */status|*/status/) : ;;
  *) CALLBACK_URL="${CALLBACK_URL%/}/status" ;;
esac

# json_string escapes its argument for embedding in a JSON string value. Pure
# bash so it works on macOS /bin/bash 3.2 (no jq, no associative arrays).
json_string() {
  local value="$1"
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '%s' "$value"
}

# post_status POSTs a status update to GARM. Reporting is best-effort: a
# callback failure must never mask the real install outcome.
post_status() {
  local status="$1" message="$2" agent_id="${3:-}"
  local payload
  if [ -n "$agent_id" ]; then
    payload="{\"status\":\"${status}\",\"message\":\"$(json_string "$message")\",\"agent_id\":${agent_id}}"
  else
    payload="{\"status\":\"${status}\",\"message\":\"$(json_string "$message")\"}"
  fi
  curl --retry 3 --retry-delay 2 --retry-connrefused --fail -s \
    -X POST -d "$payload" \
    -H 'Accept: application/json' \
    -H "@$AUTH_HDR" \
    "${CALLBACK_URL}" >/dev/null 2>&1 || true
}

send_status() { post_status "installing" "$1"; }

fail() {
  post_status "failed" "$1"
  echo "garm-macos-runner: $1" >&2
  exit 1
}

success() { post_status "idle" "$1" "${2:-}"; }

# auth_header_file writes the bearer Authorization header to a 0600 temp file
# and echoes its path. curl reads a header from a file when the -H argument
# starts with '@', so the instance token is passed to curl without ever
# landing on a process command line (ps-visible). The file is removed on exit.
auth_header_file() {
  local hdr
  hdr="$(mktemp "${TMPDIR:-/tmp}/garm-auth.XXXXXX")" \
    || fail "failed to create bearer-header temp file"
  printf 'Authorization: Bearer %s' "$BEARER_TOKEN" >"$hdr"
  chmod 600 "$hdr"
  printf '%s' "$hdr"
}

# fetch_cred downloads one JIT credential (or the service name) from the
# metadata service and fails unless it is non-empty.
fetch_cred() {
  local src="$1" dst="$2"
  curl --retry 5 --retry-delay 5 --retry-connrefused --fail -sS \
    -H 'Accept: application/json' \
    -H "@$AUTH_HDR" \
    -o "$dst" "${METADATA_URL}${src}" \
    || fail "failed to fetch ${src} from metadata service"
  [ -s "$dst" ] || fail "metadata service returned an empty ${src}"
  # The fetched file may carry the JIT RSA private key material; make it
  # owner-only (0600) before anything else reads it. curl -o creates the file
  # with the process umask (observed 0644/0664), so tighten it explicitly. The
  # process runs as the runner user, so the owner is already correct.
  chmod 600 "$dst" || fail "failed to chmod ${dst}"
}

# download_runner fetches and extracts the runner archive.
download_runner() {
  send_status "downloading runner"
  mkdir -p "$RUN_HOME" || fail "failed to create $RUN_HOME"
  curl --retry 5 --retry-delay 5 --retry-connrefused --fail -sSL \
    -o "$RUN_HOME/runner.tar.gz" "$DOWNLOAD_URL" \
    || fail "failed to download runner archive"
  send_status "extracting runner"
  tar -xf "$RUN_HOME/runner.tar.gz" -C "$RUN_HOME" \
    || fail "failed to extract runner archive"
  rm -f "$RUN_HOME/runner.tar.gz"
}

# ensure_svc_script writes ./svc.sh from the runner's darwin template unless a
# svc.sh is already present (golden image). The non-JIT path gets svc.sh from
# config.sh; the JIT path has no config.sh run, so we generate it here.
ensure_svc_script() {
  local service_name="$1" template="$RUN_HOME/bin/darwin.svc.sh.template"
  if [ -f "$RUN_HOME/svc.sh" ]; then
    return 0
  fi
  [ -f "$template" ] || fail "runner archive is missing bin/darwin.svc.sh.template"
  sed \
    -e "s|{{"{{"}}SvcNameVar{{"}}"}}|${service_name}|g" \
    -e "s|{{"{{"}}SvcDescription{{"}}"}}|GitHub Actions Runner (${service_name})|g" \
    "$template" > "$RUN_HOME/svc.sh" \
    || fail "failed to generate svc.sh from darwin.svc.sh.template"
  chmod +x "$RUN_HOME/svc.sh" || fail "failed to chmod svc.sh"
}

# assert_keychain_unlocked verifies the login keychain is unlocked before the
# launchd service starts. macOS 15+ refuses SecKey keypair operations while it
# is locked, which makes the runner service crash on start. The golden image is
# expected to unlock it at boot (auto-login or a LaunchDaemon); here we only
# assert the invariant and fail with an actionable message when it is not met.
assert_keychain_unlocked() {
  if security show-keychain-info >/dev/null 2>&1; then
    return 0
  fi
  fail "login keychain is locked: unlock it (auto-login or a boot LaunchDaemon running 'security unlock-keychain'), then re-run"
}

# extract_agent_id reads the numeric agent id from .runner, if present. It is
# only meaningful in the non-JIT path (config.sh writes .runner with an
# agentId); the JIT runner reports its own id once it connects.
extract_agent_id() {
  if [ -f "$RUN_HOME/.runner" ]; then
    sed -n 's/.*"agentId"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$RUN_HOME/.runner" | head -n 1
  fi
}

# is_configured reports whether the runner is already configured in RUN_HOME,
# mirroring the runner's own config.sh guard (Runner.Listener
# ConfigurationManager.IsConfigured: a .runner / .runner_migrated file is
# present). A runner that has been configured once must not be configured
# again, so callers use this to short-circuit to "start the service".
is_configured() {
  [ -f "$RUN_HOME/.runner" ] || [ -f "$RUN_HOME/.runner_migrated" ]
}

# Materialize the bearer Authorization header once into a 0600 temp file so
# every curl call can pass it via `-H @file`; the instance token is then never
# on a process command line. The EXIT trap removes it (and any registration
# token file) when the script finishes.
AUTH_HDR="$(auth_header_file)"
REG_TOKEN_FILE=""
cleanup() {
  rm -f "$AUTH_HDR"
  if [ -n "$REG_TOKEN_FILE" ]; then rm -f "$REG_TOKEN_FILE"; fi
}
trap cleanup EXIT

# Resolve the runner binary: skip the download when the golden image already
# ships run.sh (cache-friendly), otherwise fetch and extract it.
if [ -f "$RUN_HOME/run.sh" ]; then
  send_status "using cached runner in $RUN_HOME"
else
  download_runner
fi

cd "$RUN_HOME" || fail "failed to enter $RUN_HOME"

# Idempotency: a second run on an already-configured runner must not
# re-register (config.sh rejects "already configured") nor re-install the
# launchd job (svc.sh install fails if the plist already exists). Detect the
# configured state and just (re)start the service: launchctl load is a no-op
# on an already-running service, so a stopped service is started and a running
# one is left alone.
if is_configured; then
  send_status "runner already configured; starting service"
  assert_keychain_unlocked
  [ -f "$RUN_HOME/svc.sh" ] || fail "runner is configured but svc.sh is missing; re-provision the VM"
  ./svc.sh start || fail "failed to start launchd service"
  success "runner started" "$(extract_agent_id)"
  exit 0
fi

{{- if .UseJITConfig }}
send_status "downloading JIT credentials"
fetch_cred "credentials/runner" "$RUN_HOME/.runner"
fetch_cred "credentials/credentials" "$RUN_HOME/.credentials"
fetch_cred "credentials/credentials_rsaparams" "$RUN_HOME/.credentials_rsaparams"
fetch_cred "system/service-name" "$RUN_HOME/.service"

SVC_NAME=$(cat "$RUN_HOME/.service")
[ -n "$SVC_NAME" ] || fail "metadata service returned an empty service name"

send_status "generating launchd service"
ensure_svc_script "$SVC_NAME"

assert_keychain_unlocked
send_status "installing runner service"
./svc.sh install || fail "failed to install launchd service"
send_status "starting runner service"
./svc.sh start || fail "failed to start launchd service"

success "runner started"
{{- else }}
send_status "fetching runner registration token"
REG_TOKEN_FILE="$(mktemp "${TMPDIR:-/tmp}/garm-regtoken.XXXXXX")" \
  || fail "failed to create registration-token temp file"
curl --retry 5 --retry-delay 5 --retry-connrefused --fail -s \
  -H 'Accept: application/json' \
  -H "@$AUTH_HDR" \
  -o "$REG_TOKEN_FILE" "${METADATA_URL}runner-registration-token/" \
  || fail "failed to fetch runner registration token"
[ -s "$REG_TOKEN_FILE" ] || fail "metadata service returned an empty registration token"
chmod 600 "$REG_TOKEN_FILE"

send_status "configuring runner"
{{- if .GitHubRunnerGroup }}
./config.sh --unattended --url "$REPO_URL" --token "$(cat "$REG_TOKEN_FILE")" \
  --runnergroup "$GITHUB_RUNNER_GROUP" --name "$RUNNER_NAME" \
  --labels "$RUNNER_LABELS" --no-default-labels --ephemeral \
  || fail "failed to configure runner"
{{- else }}
./config.sh --unattended --url "$REPO_URL" --token "$(cat "$REG_TOKEN_FILE")" \
  --name "$RUNNER_NAME" --labels "$RUNNER_LABELS" \
  --no-default-labels --ephemeral \
  || fail "failed to configure runner"
{{- end }}

# The registration token has served its single use; remove it at once so it
# never lingers on disk. config.sh exposes the token via --token on its own
# argv (the runner offers no token-file/stdin/env input), so that brief argv
# window is a documented residual — the token is read from a 0600 file and
# deleted immediately after, and the JIT path avoids it entirely.
rm -f "$REG_TOKEN_FILE"
REG_TOKEN_FILE=""

assert_keychain_unlocked
send_status "installing runner service"
./svc.sh install || fail "failed to install launchd service"
send_status "starting runner service"
./svc.sh start || fail "failed to start launchd service"

success "runner started" "$(extract_agent_id)"
{{- end }}
