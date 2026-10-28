#!/bin/bash
# macOS runner-install template. GARM renders this Go text/template with
# cloudconfig.InstallRunnerParams (os_type=linux, since GARM has no darwin
# type) and the rendered script runs inside the guest under /bin/bash 3.2.
#
# bash 3.2 constraints this template is written against (proven by the
# test/bash32 lane, not just shellcheck + a bash 5 dry run):
#   - no associative arrays (`declare -A`), no `mapfile`/`readarray`
#   - no `${var,,}` / `${var^^}` case modification
#   - no `sed -i` without a suffix (BSD sed); no systemd, no
#     installdependencies.sh, no useradd

set -e
set -o pipefail

{{- if .EnableBootDebug }}
set -x
{{- end }}

CALLBACK_URL="{{ .CallbackURL }}"
METADATA_URL="{{ .MetadataURL }}"
BEARER_TOKEN="{{ .CallbackToken }}"

RUN_HOME="/Users/{{ .RunnerUsername }}/actions-runner"

if [ -z "$METADATA_URL" ]; then
	echo "no METADATA_URL is set"
	exit 1
fi

function call() {
	PAYLOAD="$1"
	curl --retry 5 --retry-delay 5 --retry-connrefused --fail -s -X POST -d "${PAYLOAD}" \
		-H 'Accept: application/json' -H "Authorization: Bearer ${BEARER_TOKEN}" \
		"${CALLBACK_URL}" || echo "failed to call home: exit code ($?)"
}

function sendStatus() {
	MSG="$1"
	call "{\"status\": \"installing\", \"message\": \"$MSG\"}"
}

function success() {
	MSG="$1"
	ID=${2:-null}
	call "{\"status\": \"idle\", \"message\": \"$MSG\", \"agent_id\": $ID}"
}

function fail() {
	MSG="$1"
	call "{\"status\": \"failed\", \"message\": \"$MSG\"}"
	exit 1
}

function getURL() {
	# $1 = path relative to METADATA_URL, $2 = output file
	curl --retry 5 --retry-delay 5 --retry-connrefused --fail -s \
		-X GET -H 'Accept: application/json' -H "Authorization: Bearer ${BEARER_TOKEN}" \
		"${METADATA_URL}/$1" -o "$2"
}

# macOS 15+ refuses to start VMs with a locked login keychain. Assert the
# keychain is unlocked (or unlock it) before the runner service starts.
function ensureKeychain() {
	if ! security unlock-keychain login.keychain-db 2>/dev/null; then
		fail "login keychain is locked; unlock it before starting the runner"
	fi
}

function downloadAndExtractRunner() {
	sendStatus "downloading tools from {{ .DownloadURL }}"
	curl --retry 5 --retry-delay 5 --retry-connrefused --fail -L \
		-o "/Users/{{ .RunnerUsername }}/{{ .FileName }}" "{{ .DownloadURL }}" \
		|| fail "failed to download tools"
	mkdir -p "$RUN_HOME" || fail "failed to create actions-runner folder"
	sendStatus "extracting runner"
	tar xf "/Users/{{ .RunnerUsername }}/{{ .FileName }}" -C "$RUN_HOME"/ || fail "failed to extract runner"
	chown -R {{ .RunnerUsername }}:{{ .RunnerGroup }} "$RUN_HOME"/ || fail "failed to change owner"
}

# Cache-friendly: golden images ship the runner binary, so a cached runner
# skips the download entirely.
if [ -f "$RUN_HOME/run.sh" ]; then
	sendStatus "using cached runner found in $RUN_HOME"
else
	downloadAndExtractRunner
fi

cd "$RUN_HOME" || fail "failed to enter $RUN_HOME"

sendStatus "configuring runner"
{{- if .UseJITConfig }}

sendStatus "downloading JIT credentials"
getURL "credentials/runner" ".runner" || fail "failed to get runner file"
getURL "credentials/credentials" ".credentials" || fail "failed to get credentials file"
getURL "credentials/credentials_rsaparams" ".credentials_rsaparams" || fail "failed to get credentials_rsaparams file"
getURL "system/service-name" ".service" || fail "failed to get service name file"
SVC_NAME=$(cat .service)

ensureKeychain

sendStatus "installing runner service"
./svc.sh install || fail "failed to install service"
./svc.sh start || fail "failed to start service"

if ! launchctl list "$SVC_NAME" >/dev/null 2>&1; then
	fail "launchd service $SVC_NAME did not start"
fi

# JIT runners self-register; GARM learns the agent id from the runner itself.
AGENT_ID=""
{{- else }}

sendStatus "fetching registration token"
GITHUB_TOKEN=$(curl --retry 5 --retry-delay 5 --retry-connrefused --fail -s \
	-X GET -H 'Accept: application/json' -H "Authorization: Bearer ${BEARER_TOKEN}" \
	"${METADATA_URL}/runner-registration-token/") || fail "failed to get registration token"

# Idempotent: an already-configured runner keeps its .runner file and is not
# re-registered; the service is (re)started if it stopped.
if [ ! -f ".runner" ]; then
	sendStatus "configuring runner"
	{{- if .GitHubRunnerGroup }}
	./config.sh --unattended --url "{{ .RepoURL }}" --token "$GITHUB_TOKEN" --runnergroup {{ .GitHubRunnerGroup }} --name "{{ .RunnerName }}" --labels "{{ .RunnerLabels }}" --no-default-labels --ephemeral || fail "failed to configure runner"
	{{- else }}
	./config.sh --unattended --url "{{ .RepoURL }}" --token "$GITHUB_TOKEN" --name "{{ .RunnerName }}" --labels "{{ .RunnerLabels }}" --no-default-labels --ephemeral || fail "failed to configure runner"
	{{- end }}
else
	sendStatus "runner already configured"
fi

ensureKeychain

sendStatus "installing runner service"
./svc.sh install || fail "failed to install service"
./svc.sh start || fail "failed to start service"

set +e
AGENT_ID=$(grep "agentId" .runner | tr -d -c 0-9)
if [ $? -ne 0 ]; then
	fail "failed to get agent ID"
fi
set -e
{{- end }}

success "runner successfully installed" $AGENT_ID
