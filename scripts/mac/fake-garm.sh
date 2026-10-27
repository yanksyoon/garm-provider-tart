#!/usr/bin/env bash
# Drive the provider exactly as GARM does: environment variables in, one JSON
# document (or nothing) on stdout, exit code out. No Mac and no Tart needed.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

binary="$workdir/garm-provider-tart"
go build -o "$binary" "$repo_root/cmd/garm-provider-tart"

config="$workdir/provider.toml"
cat >"$config" <<'TOML'
ssh_user = "garm"
slots_per_host = 2

[[hosts]]
name = "mac-mini-01"
address = "10.20.0.11"
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
