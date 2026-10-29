package hostctl

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunRejectsSlotsAboveLicenceCap proves the CLI path (which reads
// TART_FLEET_SLOTS_PER_HOST into CLIEnv.SlotsPerHost) rejects an over-cap value
// before any Tart command runs, matching the NewService ceiling.
func TestRunRejectsSlotsAboveLicenceCap(t *testing.T) {
	// Arrange: an over-cap TART_FLEET_SLOTS_PER_HOST.
	var stdout, stderr bytes.Buffer

	// Act: run a status verb with SlotsPerHost = 5.
	code := Run([]string{"status"}, CLIEnv{SlotsPerHost: 5}, strings.NewReader(""), &stdout, &stderr)

	// Assert: a failure exit with a validation fault and no JSON on stdout.
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), FaultValidation) {
		t.Fatalf("stderr = %q, want a %q fault", stderr.String(), FaultValidation)
	}
}
