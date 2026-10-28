#!/bin/sh
# Stub `config.sh` — non-JIT runner registration. Writes the .runner (with an
# agentId) and .credentials files the template reads back, and records argv.
set -u

LOG="${CONFIG_SH_LOG:-/dev/null}"
printf 'config.sh %s\n' "$*" >> "$LOG"

cat > .runner <<'EOF'
{"agentId": 4242, "agentName": "garm-abc123"}
EOF
cat > .credentials <<'EOF'
{"scheme": "OAuth", "token": "fake"}
EOF
exit 0
