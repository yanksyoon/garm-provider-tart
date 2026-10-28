#!/bin/sh
# Stub `svc.sh` — the runner's own launchd service script (ships in the
# runner archive). Records the verb so tests can assert install/start ran,
# and models "already installed" idempotency by returning 0.
set -u

LOG="${SVC_SH_LOG:-/dev/null}"
printf 'svc.sh %s\n' "$*" >> "$LOG"

case "${1:-}" in
	install) echo "svc.sh: installed"; exit 0 ;;
	start)   echo "svc.sh: started";  exit 0 ;;
	stop)    echo "svc.sh: stopped";  exit 0 ;;
	*) echo "svc.sh: usage: $0 install|start|stop" >&2; exit 2 ;;
esac
