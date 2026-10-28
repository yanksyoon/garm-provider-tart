# garm-provider-tart build targets.

# integration drives the real GARM controller against the real provider binary,
# two fake hosts (real tart-hostctl + stub tart + real sshd) and a real Gitea
# forge. The test driver brings the compose stack up, runs, and tears it down
# (`docker compose down -v`) even on failure. Requires Docker (or set
# GARM_TEST_COMPOSE=podman-compose for a rootless local run).
.PHONY: integration
integration:
	go test -tags integration ./test/integration/... -count=1 -v

# contract replays the pinned GARM external-provider contract against the
# provider (TestReplay) and, when GARM_CONTRACT_CAPTURE points at a fresh
# capture, diffs the shape against the pinned fixtures (TestCanaryDrift).
# See test/contract/ and .github/workflows/contract-canary.yml.
.PHONY: contract
contract:
	go test -tags contract ./test/contract/... -count=1 -v

# contract-capture regenerates the pinned fixtures for a GARM ref by building
# upstream cloudbase/garm at that ref and driving its external provider against
# a recording shim. GARM_REF is required (a tag or sha), e.g.:
#
#   make contract-capture GARM_REF=33ec092
#
# The fixtures land under test/contract/fixtures/$(GARM_REF)/. Review the diff,
# then commit it as the new pin.
.PHONY: contract-capture
contract-capture:
	@test -n "$(GARM_REF)" || { echo "GARM_REF is required: make contract-capture GARM_REF=<sha>" >&2; exit 2; }
	./scripts/contract/capture.sh "$(GARM_REF)" "$(CURDIR)/test/contract/fixtures/$(GARM_REF)"

# test-bash32 renders the macOS runner-install template exactly as GARM does
# and runs it under a source-built bash 3.2 in a container (no Mac, no network
# at run time). The image is built in CI and cached; the run refuses to start
# unless the image's bash is exactly 3.2.x. Requires a container runtime —
# set CONTAINER_RUNTIME=podman for a rootless local run.
CONTAINER_RUNTIME ?= docker
.PHONY: test-bash32
test-bash32:
	$(CONTAINER_RUNTIME) build -t garm-provider-tart:bash32 -f test/bash32/Dockerfile .
	$(CONTAINER_RUNTIME) run --rm garm-provider-tart:bash32
