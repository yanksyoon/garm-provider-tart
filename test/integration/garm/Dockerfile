# GARM controller for the integration test. The published GARM image is
# busybox-based and has no SSH client, but the Tart provider shells out to
# `ssh` to reach the fake hosts, so layer an OpenSSH client onto an Alpine
# base and copy in the (static) GARM binaries. curl is added so the test driver
# can drive the Gitea API (create the repo and PAT) over the compose network.
FROM alpine:3.20

RUN apk add --no-cache openssh-client ca-certificates curl

COPY --from=ghcr.io/cloudbase/garm:v0.2.1 /bin/garm /bin/garm
COPY --from=ghcr.io/cloudbase/garm:v0.2.1 /bin/garm-cli /bin/garm-cli

ENTRYPOINT ["/bin/garm", "-config", "/etc/garm/config.toml"]
