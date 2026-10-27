package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	commonParams "github.com/cloudbase/garm-provider-common/params"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// providerConfig returns a valid config whose ssh_bin is the test stub.
func providerConfig(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`
ssh_user = "garm"
ssh_bin = %q
hostctl_bin = "/usr/local/bin/tart-hostctl"
image = "ghcr.io/cirruslabs/macos-tahoe-xcode@sha256:abc"
slots_per_host = 2

[[hosts]]
name = "mac-mini-01"
address = "10.20.0.11"

[[hosts]]
name = "mac-mini-02"
address = "10.20.0.12"
`, sshStubPath(t))
}

func env(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func runWith(t *testing.T, environ map[string]string, stdin string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(env(environ), strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	// Arrange: a command GARM never sends.
	// Act: run it.
	// Assert: usage exit code and no JSON on stdout, so GARM cannot misparse it.
	code, stdout, stderr := runWith(t, map[string]string{"GARM_COMMAND": "Nope"}, "")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown GARM_COMMAND") {
		t.Fatalf("stderr = %q, want an unknown-command message", stderr)
	}
}

// TestRunRecognizesStartStopCommands locks the exact GARM_COMMAND spellings for
// Start and Stop: GARM sends StartInstance/StopInstance (see garm-provider-common
// execution/common), not Start/Stop. The old spellings must be rejected as
// unknown so a mismatched GARM never silently no-ops.
func TestRunRecognizesStartStopCommands(t *testing.T) {
	// Arrange/Act: the commands GARM actually sends, without a config (so the
	// recognised-but-unimplemented path fails later, with the failure exit).
	for _, cmd := range []string{CommandStart, CommandStop} {
		code, stdout, stderr := runWith(t, map[string]string{"GARM_COMMAND": cmd}, "")
		// Assert: recognised (not the usage exit); stdout stays empty.
		if code == exitUsage {
			t.Fatalf("GARM_COMMAND=%q not recognised: exit %d (stderr: %s)", cmd, code, stderr)
		}
		if stdout != "" {
			t.Fatalf("GARM_COMMAND=%q stdout = %q, want empty", cmd, stdout)
		}
	}

	// Arrange/Act: the spellings GARM never sends.
	for _, cmd := range []string{"Start", "Stop"} {
		code, _, _ := runWith(t, map[string]string{"GARM_COMMAND": cmd}, "")
		// Assert: rejected as an unknown command.
		if code != exitUsage {
			t.Fatalf("GARM_COMMAND=%q exit = %d, want %d (unknown command)", cmd, code, exitUsage)
		}
	}
}

func TestRunRequiresConfig(t *testing.T) {
	// Arrange: a create call without a config path.
	// Act: run it.
	// Assert: failure exit code, no panic, empty stdout.
	code, stdout, _ := runWith(t, map[string]string{"GARM_COMMAND": CommandCreateInstance}, "{}")
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestDeleteUnknownInstanceIsNoOp(t *testing.T) {
	// Arrange: GARM retries a delete for a VM that never existed; hostctl
	// reports an absent VM as success.
	t.Setenv("SSH_STUB_EXIT", "0")
	cfg := writeConfig(t, providerConfig(t))
	code, stdout, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandDeleteInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_INSTANCE_ID":          "garm-gone000",
	}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr %q), want %d", code, stderr, exitOK)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestDeleteHostUnreachableReturnsHardError(t *testing.T) {
	// Arrange: the host is unreachable during a delete.
	t.Setenv("SSH_STUB_EXIT", "255")
	cfg := writeConfig(t, providerConfig(t))
	code, stdout, _ := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandDeleteInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_INSTANCE_ID":          "mac-mini-01/garm-abc123",
	}, "")
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestGetInstanceRoundTripsProviderID(t *testing.T) {
	// Arrange: an instance GARM knows by its provider ID; hostctl reports it.
	t.Setenv("SSH_STUB_OUTPUT", writeStubFile(t,
		`{"name":"garm-xyz789","provider_id":"mac-mini-02/garm-xyz789","state":"ready","ip":"192.168.64.7"}`))
	cfg := writeConfig(t, providerConfig(t))
	code, stdout, _ := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandGetInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_INSTANCE_ID":          "mac-mini-02/garm-xyz789",
	}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	var instance commonParams.ProviderInstance
	if err := json.Unmarshal([]byte(stdout), &instance); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if instance.ProviderID != "mac-mini-02/garm-xyz789" {
		t.Fatalf("provider_id = %q, want mac-mini-02/garm-xyz789", instance.ProviderID)
	}
}

func TestConfigRejectsLicenceBreach(t *testing.T) {
	// Arrange: three guests per host, which the macOS licence forbids.
	// Act: load the config.
	// Assert: validation fails instead of over-provisioning.
	cfg := writeConfig(t, `
slots_per_host = 3

[[hosts]]
name = "mac-mini-01"
address = "10.20.0.11"
`)
	if _, err := LoadConfig(cfg); err == nil {
		t.Fatal("expected an error for slots_per_host = 3")
	}
}
