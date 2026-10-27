#!/usr/bin/env bash
# Capture the GARM external-provider contract at a given ref into fixtures.
#
# Usage: capture.sh <garm-ref> <output-dir> [garm-checkout]
#
#   <garm-ref>       tag or sha used to name the fixture dir and manifest
#   <output-dir>     absolute path where the fixture files are written
#   [garm-checkout]  optional path to an existing garm checkout; when omitted a
#                    shallow clone of cloudbase/garm at <garm-ref> is made under
#                    the system temp dir and removed on exit
#
# The capture driver (test/contract/capture) is built against the checkout via a
# `replace` directive in a throwaway modfile, so the committed go.mod stays
# pinned and clean. Nothing is written outside <output-dir> and the temp dir.
set -euo pipefail

ref="${1:?usage: capture.sh <garm-ref> <output-dir> [garm-checkout]}"
out="${2:?usage: capture.sh <garm-ref> <output-dir> [garm-checkout]}"
checkout="${3:-}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
capture_dir="$repo_root/test/contract/capture"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

if [[ -z "$checkout" ]]; then
  checkout="$scratch/garm"
  git clone --quiet --filter=blob:none --no-checkout https://github.com/cloudbase/garm "$checkout"
  git -C "$checkout" checkout --quiet "$ref"
fi

commit="$(git -C "$checkout" rev-parse HEAD)"

# Build the capture driver against the checkout: copy the committed go.mod and
# append a replace pointing at the checkout, then run with that throwaway
# modfile (its matching .sum is generated alongside). -mod=mod lets Go record
# any modules the checkout requires that the pinned go.sum does not yet know.
cp "$capture_dir/go.mod" "$scratch/go.capture.mod"
printf 'replace github.com/cloudbase/garm => %s\n' "$checkout" >>"$scratch/go.capture.mod"

(
  cd "$capture_dir"
  go run -mod=mod -modfile="$scratch/go.capture.mod" . \
    -ref "$ref" -commit "$commit" -out "$out"
)

echo "captured $ref ($commit) into $out"
