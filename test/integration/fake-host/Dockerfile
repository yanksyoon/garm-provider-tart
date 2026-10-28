# The integration fake host: a "fake Mac" that runs the real tart-hostctl
# binary (built from source by the test driver and mounted at
# /usr/local/bin/tart-hostctl) plus a stub `tart` CLI, real sshd, and the
# macOS health-probe stubs the host driver expects.
FROM alpine:3.20

RUN apk add --no-cache openssh

# Stub `tart` (fake VM lifecycle) and the macOS health-probe stubs.
COPY fake-host/tart /usr/local/bin/tart
COPY fake-host/security /usr/local/bin/security
COPY fake-host/df /usr/local/bin/df

COPY fake-host/sshd_config /etc/ssh/sshd_config
COPY fake-host/entrypoint.sh /entrypoint.sh

RUN addgroup -S garm \
 && adduser -S -D -s /bin/sh -G garm -h /home/garm garm \
 && passwd -u garm \
 && mkdir -p /home/garm/.ssh /var/db/tart-fleet /var/lib/tart-fake \
 && chmod 755 /usr/local/bin/tart /usr/local/bin/security /usr/local/bin/df /entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/entrypoint.sh"]
