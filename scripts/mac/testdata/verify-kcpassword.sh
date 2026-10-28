#!/usr/bin/env bash
# Verify kcpassword_bytes() round-trips against the classic macOS XOR key.
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
# shellcheck source=scripts/mac/lib.sh
. "$repo_root/scripts/mac/lib.sh"
kcpassword_bytes "admin" > /tmp/kc-verify.bin
python3 - <<'PY'
key = [0x7D,0x89,0x52,0x23,0xD2,0xBC,0xDD,0xEA,0xA3,0xB9,0x1F]
data = open("/tmp/kc-verify.bin","rb").read()
assert len(data) == 12, "kcpassword must be 12 bytes"
pw = bytes(data[i] ^ key[i % 11] for i in range(len(data)))
print("decoded:", repr(pw))
assert pw.rstrip(b"\x00") == b"admin", "MISMATCH"
print("kcpassword round-trip OK")
PY
rm -f /tmp/kc-verify.bin
