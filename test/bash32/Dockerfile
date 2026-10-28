# syntax=docker/dockerfile:1
#
# Builds a bash 3.2 image and runs the macOS runner-install template through
# it. Stage 1 compiles the template renderer (which calls
# cloudconfig.InstallRunnerScript, the exact function GARM uses); stage 2
# source-builds bash 3.2.57 from a pinned tarball; stage 3 assembles a runtime
# with bash 3.2 as the only `bash` and no curl/build tools, so removing a stub
# surfaces as "command not found" instead of falling back to a real binary.
# All network access happens at build time; `docker run` is fully offline.

# --- Stage 1: the template renderer ---
FROM docker.io/library/golang:1.26-bookworm AS renderer
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY test/bash32/render ./test/bash32/render
RUN CGO_ENABLED=0 go build -o /out/render ./test/bash32/render

# --- Stage 2: source-build bash 3.2.57 ---
FROM docker.io/library/debian:bookworm-slim AS bash-builder
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential bison ca-certificates curl \
 && curl -fsSL https://ftp.gnu.org/gnu/bash/bash-3.2.57.tar.gz -o /tmp/bash-3.2.57.tar.gz \
 && echo "3fa9daf85ebf35068f090ce51283ddeeb3c75eb5bc70b1a4a7cb05868bfe06a4  /tmp/bash-3.2.57.tar.gz" | sha256sum -c - \
 && tar xzf /tmp/bash-3.2.57.tar.gz -C /tmp \
 && cd /tmp/bash-3.2.57 \
 && ./configure --prefix=/usr/local --without-bash-malloc \
 && make \
 && make install \
 && rm -rf /tmp/bash-3.2.57 /tmp/bash-3.2.57.tar.gz

# --- Stage 3: bash 3.2 runtime + the harness ---
FROM docker.io/library/debian:bookworm-slim AS bash32
# `passwd` supplies useradd/groupadd for the runner user the template chowns
# to; coreutils/sed/grep/tar ship with the base image.
RUN apt-get update \
 && apt-get install -y --no-install-recommends passwd \
 && rm -rf /var/lib/apt/lists/*

COPY --from=bash-builder /usr/local/bin/bash /usr/local/bin/bash
RUN ln -sf /usr/local/bin/bash /bin/bash

COPY --from=renderer /out/render /usr/local/bin/render
COPY test/bash32/template /opt/bash32/template
COPY test/bash32/stubs /opt/bash32/stubs
COPY test/bash32/fixtures /opt/bash32/fixtures
COPY test/bash32/runner /opt/bash32/runner
COPY test/bash32/run.sh /opt/bash32/run.sh

RUN chmod +x /opt/bash32/stubs/* /opt/bash32/runner/* /opt/bash32/run.sh

ENTRYPOINT ["/opt/bash32/run.sh"]
