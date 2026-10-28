#!/bin/sh
# Entrypoint for the integration fake host: prepare the `garm` user's SSH
# environment, then run sshd in the foreground.
set -eu

# The provider's public key is bind-mounted read-only at
# /run/garm-authorized_keys by the compose file, owned by the container root.
# Copy it into place with the garm owner so the privilege-separated sshd child
# (which runs as garm, not root) can read it.
mkdir -p /home/garm/.ssh
chmod 700 /home/garm/.ssh
chown garm:garm /home/garm/.ssh 2>/dev/null || true
if [ -f /run/garm-authorized_keys ]; then
  cp /run/garm-authorized_keys /home/garm/.ssh/authorized_keys
  chown garm:garm /home/garm/.ssh/authorized_keys 2>/dev/null || true
  chmod 600 /home/garm/.ssh/authorized_keys
fi

# State roots for the host driver (registry) and the stub tart. The provider
# connects as `garm`, so that user owns both.
mkdir -p /var/db/tart-fleet /var/lib/tart-fake
chown -R garm:garm /var/db/tart-fleet /var/lib/tart-fake 2>/dev/null || true

# Host keys on first boot.
if [ ! -f /etc/ssh/ssh_host_ed25519_key ]; then
  ssh-keygen -A
fi

# The remote command is /usr/local/bin/tart-hostctl; its `tart`, `security` and
# `df` stubs live in /usr/local/bin, so the session PATH must include it.
cat >/home/garm/.ssh/environment <<EOF
PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
EOF
chown garm:garm /home/garm/.ssh/environment 2>/dev/null || true
chmod 600 /home/garm/.ssh/environment

exec /usr/sbin/sshd -D -e
