#!/usr/bin/env bash
# Drive the provider exactly as GARM does: environment variables in, one JSON
# document (or nothing) on stdout, exit code out. No Mac and no Tart needed:
# the provider's ssh transport is pointed at testdata/bin/ssh, which replays
# canned hostctl output.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

binary="$workdir/garm-provider-tart"
go build -o "$binary" "$repo_root/cmd/garm-provider-tart"

# Canned hostctl responses replayed by the ssh stub, routed by verb so the
# probe-then-create flow works.
printf '%s\n' '{"tart_version":"tart 2.0.0","keychain_unlocked":true,"free_disk_bytes":100000000000,"drained":false,"capacity":{"total":2,"used":0,"adopted":0,"quarantined":0},"instances":[]}' \
  >"$workdir/status.json"
printf '%s\n' '{"name":"garm-abc123","provider_id":"mac-mini-01/garm-abc123","state":"ready","ip":"192.168.64.5"}' \
  >"$workdir/instance.json"
printf '%s\n' '[]' >"$workdir/instances.json"
export SSH_STUB_STATUS_OUTPUT="$workdir/status.json"
export SSH_STUB_CREATE_OUTPUT="$workdir/instance.json"
export SSH_STUB_GET_OUTPUT="$workdir/instance.json"
export SSH_STUB_LIST_OUTPUT="$workdir/instances.json"
export SSH_STUB_EXIT="0"

config="$workdir/provider.toml"
cat >"$config" <<TOML
ssh_user = "garm"
ssh_bin = "$repo_root/testdata/bin/ssh"
hostctl_bin = "/usr/local/bin/tart-hostctl"
image = "ghcr.io/cirruslabs/macos-tahoe-xcode@sha256:abc"
slots_per_host = 2

[[hosts]]
name = "mac-mini-01"
address = "10.20.0.11"

[[hosts]]
name = "mac-mini-02"
address = "10.20.0.12"
TOML

export GARM_PROVIDER_CONFIG_FILE="$config"
export GARM_CONTROLLER_ID="controller-1"
export GARM_POOL_ID="9dcf590a-1192-4a9c-b3e4-e0902974c2c0"

bootstrap='{"name":"garm-abc123","os_type":"linux","arch":"arm64","pool_id":"9dcf590a-1192-4a9c-b3e4-e0902974c2c0","repo_url":"https://github.com/yanksyoon/garm-provider-tart","extra_specs":{"cpu":8}}'

cmd=${1:-create}

case "$cmd" in
  create)
    printf '%s' "$bootstrap" | GARM_COMMAND=CreateInstance "$binary"
    ;;
  list)
    GARM_COMMAND=ListInstances "$binary"
    ;;
  get)
    GARM_INSTANCE_ID="mac-mini-01/garm-abc123" GARM_COMMAND=GetInstance "$binary"
    ;;
  delete)
    GARM_INSTANCE_ID="mac-mini-01/garm-abc123" GARM_COMMAND=DeleteInstance "$binary"
    ;;
  delete-unknown)
    GARM_INSTANCE_ID="garm-gone000" GARM_COMMAND=DeleteInstance "$binary"
    ;;
  unknown)
    GARM_COMMAND=Nope "$binary"
    ;;
  *)
    echo "fake-garm: known subcommands are create, list, get, delete, delete-unknown, unknown" >&2
    exit 64
    ;;
esac
