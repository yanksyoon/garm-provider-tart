# garm-provider-tart build targets.

# integration drives the real GARM controller against the real provider binary,
# two fake hosts (real tart-hostctl + stub tart + real sshd) and a real Gitea
# forge. The test driver brings the compose stack up, runs, and tears it down
# (`docker compose down -v`) even on failure. Requires Docker (or set
# GARM_TEST_COMPOSE=podman-compose for a rootless local run).
.PHONY: integration

integration:
	go test -tags integration ./test/integration/... -count=1 -v
