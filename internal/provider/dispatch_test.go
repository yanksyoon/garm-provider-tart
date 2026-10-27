package provider

import (
	"bytes"
	"encoding/json"
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

const validConfig = `
ssh_user = "garm"
slots_per_host = 2

[[hosts]]
name = "mac-mini-01"
address = "10.20.0.11"

[[hosts]]
name = "mac-mini-02"
address = "10.20.0.12"
`

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

func TestCreateInstancePrintsProviderInstance(t *testing.T) {
	// Arrange: a valid config and a bootstrap payload shaped like GARM's.
	// Act: create an instance.
	// Assert: one JSON object with a host-qualified provider ID and running status.
	cfg := writeConfig(t, validConfig)
	bootstrap, err := json.Marshal(commonParams.BootstrapInstance{
		Name:       "garm-abc123",
		RepoURL:    "https://github.com/yanksyoon/garm-provider-tart",
		PoolID:     "9dcf590a-1192-4a9c-b3e4-e0902974c2c0",
		OSType:     commonParams.Linux,
		OSArch:     commonParams.Arm64,
		ExtraSpecs: json.RawMessage(`{"cpu":8}`),
	})
	if err != nil {
		t.Fatalf("marshal bootstrap: %v", err)
	}

	code, stdout, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandCreateInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_CONTROLLER_ID":        "controller-1",
		"GARM_POOL_ID":              "9dcf590a-1192-4a9c-b3e4-e0902974c2c0",
	}, string(bootstrap))
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr %q), want %d", code, stderr, exitOK)
	}

	var instance commonParams.ProviderInstance
	if err := json.Unmarshal([]byte(stdout), &instance); err != nil {
		t.Fatalf("stdout is not one JSON object: %v (%q)", err, stdout)
	}
	if instance.ProviderID != "mac-mini-01/garm-abc123" {
		t.Fatalf("provider_id = %q, want mac-mini-01/garm-abc123", instance.ProviderID)
	}
	if instance.Status != commonParams.InstanceRunning {
		t.Fatalf("status = %q, want %q", instance.Status, commonParams.InstanceRunning)
	}
	if len(instance.Addresses) == 0 {
		t.Fatal("no addresses reported")
	}
}

func TestCreateInstanceRejectsEmptyStdin(t *testing.T) {
	// Arrange: an empty stdin, which happens when a caller forgets the payload.
	// Act: create an instance.
	// Assert: failure exit code and an actionable message.
	cfg := writeConfig(t, validConfig)
	code, _, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandCreateInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
	}, "")
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "bootstrap") {
		t.Fatalf("stderr = %q, want a bootstrap decode error", stderr)
	}
}

func TestListInstancesReturnsEmptyArray(t *testing.T) {
	// Arrange: a valid config and no instances.
	// Act: list.
	// Assert: `[]`, not `null`, because GARM unmarshals into a slice.
	cfg := writeConfig(t, validConfig)
	code, stdout, _ := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandListInstances,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_POOL_ID":              "9dcf590a-1192-4a9c-b3e4-e0902974c2c0",
	}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("stdout = %q, want []", strings.TrimSpace(stdout))
	}
}

func TestDeleteUnknownInstanceIsNoOp(t *testing.T) {
	// Arrange: GARM retries a delete for a VM that never existed.
	// Act: delete it.
	// Assert: success and no output, so the retry is not blocked.
	cfg := writeConfig(t, validConfig)
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

func TestGetInstanceRoundTripsProviderID(t *testing.T) {
	// Arrange: an instance GARM knows by its provider ID.
	// Act: get it.
	// Assert: the same provider ID comes back, so GARM can reconcile state.
	cfg := writeConfig(t, validConfig)
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
