#!/usr/bin/env bash
# Install and load the boot LaunchDaemon that unlocks the Tart user's login
# keychain. macOS 15+ refuses to start VMs while the login keychain is locked
# (SecKeyCreateRandomEntry_ios / "Failed to generate keypair"), so this daemon
# must run at boot before the provider can start a VM.
#
# Ordering: a LaunchDaemon in /Library/LaunchDaemons is loaded by launchd at
# boot, before any user session and therefore before GARM reaches the host over
# SSH and starts a VM. RunAtLoad makes the unlock happen immediately at load,
# not after login.
#
# Idempotent: a second run is a no-op and exits 0. --dry-run prints the action
# and changes nothing. The password is never written anywhere: the daemon reads
# it from a root-only 0600 file at load time (KEYCHAIN_PW_FILE) and passes it
# to `security unlock-keychain` on stdin, never via the ps-visible `-p` flag.
#
# Usage:
#   sudo scripts/mac/keychain-unlock-daemon.sh --runner-user garm
#   scripts/mac/keychain-unlock-daemon.sh --dry-run
set -euo pipefail

PROG="keychain-unlock-daemon"

RUNNER_USER="${RUNNER_USER:-garm}"
KEYCHAIN_PW_FILE="${KEYCHAIN_PW_FILE:-/var/root/.keychain-pw}"
LAUNCHD_DIR="${LAUNCHD_DIR:-/Library/LaunchDaemons}"
WRAPPER_PATH="${WRAPPER_PATH:-/usr/local/bin/garm-keychain-unlock}"
LABEL="${LABEL:-com.garm.keychain-unlock}"

usage() {
  cat <<EOF
Usage: $PROG [--dry-run] [--runner-user <user>] [--keychain-pw-file <path>]
             [--launchd-dir <dir>] [--wrapper-path <path>] [--label <label>]

Installs a boot LaunchDaemon that unlocks <user>'s login keychain at load.
EOF
}

DRY_RUN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --runner-user) RUNNER_USER="$2"; shift ;;
    --keychain-pw-file) KEYCHAIN_PW_FILE="$2"; shift ;;
    --launchd-dir) LAUNCHD_DIR="$2"; shift ;;
    --wrapper-path) WRAPPER_PATH="$2"; shift ;;
    --label) LABEL="$2"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

die() { echo "$PROG: $1" >&2; exit 1; }
log() { echo "$PROG: $1"; }

KEYCHAIN_PATH="/Users/$RUNNER_USER/Library/Keychains/login.keychain-db"
PLIST="$LAUNCHD_DIR/$LABEL.plist"

configured() {
  [ -f "$PLIST" ] && [ -x "$WRAPPER_PATH" ] && launchctl list 2>/dev/null | grep -qF "$LABEL"
}

if configured; then
  log "already configured ($LABEL)"
  exit 0
fi

if [ "$DRY_RUN" -eq 1 ]; then
  log "would install $WRAPPER_PATH and $PLIST and load $LABEL"
  exit 0
fi

# Root/sudo is required to write into LAUNCHD_DIR and load a system daemon.
if [ "$(id -u)" != "0" ] && ! sudo -n true 2>/dev/null; then
  die "sudo required (run as root or with passwordless sudo)"
fi

# The password file must already exist (root-only, 0600) or the daemon would be
# installed broken. Reference it by path; never copy the secret into the
# wrapper or the plist.
[ -r "$KEYCHAIN_PW_FILE" ] || die "keychain password file not found at $KEYCHAIN_PW_FILE (create it root-only 0600 first)"

# The wrapper reads the password at load time. Paths are baked in, but they are
# not secrets.
sudo install -d -m 755 "$(dirname "$WRAPPER_PATH")" || die "failed to create $(dirname "$WRAPPER_PATH")"
sudo tee "$WRAPPER_PATH" >/dev/null <<EOF || die "failed to write $WRAPPER_PATH"
#!/bin/bash
# Unlock the login keychain at boot so macOS 15+ allows Tart to start VMs.
# The password is read from $KEYCHAIN_PW_FILE (root-only, 0600) at load time
# and passed to security(1) on stdin. The -p flag is deliberately not used:
# it puts the password on the security process command line, visible in ps.
# security unlock-keychain reads the password from stdin when -p is omitted.
set -euo pipefail
pw="\$(cat '$KEYCHAIN_PW_FILE')"
printf '%s\n' "\$pw" | security unlock-keychain '$KEYCHAIN_PATH'
EOF
sudo chmod 700 "$WRAPPER_PATH" || die "failed to chmod $WRAPPER_PATH"

sudo install -d -m 755 "$LAUNCHD_DIR" || die "failed to create $LAUNCHD_DIR"
sudo tee "$PLIST" >/dev/null <<EOF || die "failed to write $PLIST"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>$WRAPPER_PATH</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>UserName</key>
  <string>root</string>
</dict>
</plist>
EOF
sudo chmod 644 "$PLIST" || die "failed to chmod $PLIST"

sudo launchctl bootstrap system "$PLIST" 2>/dev/null || sudo launchctl load -w "$PLIST" \
  || die "failed to load $PLIST"

log "installed and loaded $LABEL"
