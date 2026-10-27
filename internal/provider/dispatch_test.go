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
	// Arrange: a stub ssh replaying a ready hostctl instance, and a bootstrap
	// payload shaped like GARM's.
	t.Setenv("SSH_STUB_OUTPUT", writeStubFile(t,
		`{"name":"garm-abc123","provider_id":"mac-mini-01/garm-abc123","state":"ready","ip":"192.168.64.5"}`))
	cfg := writeConfig(t, providerConfig(t))
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

	// Act: create.
	code, stdout, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandCreateInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_CONTROLLER_ID":        "controller-1",
		"GARM_POOL_ID":              "9dcf590a-1192-4a9c-b3e4-e0902974c2c0",
	}, string(bootstrap))

	// Assert: exit 0, one JSON object with the host-qualified ID and running.
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

func TestCreateExecFailureReportsErrorNotHealthy(t *testing.T) {
	// Arrange: hostctl reports state running with a last_error (the readiness
	// probe failed), never ready.
	t.Setenv("SSH_STUB_OUTPUT", writeStubFile(t,
		`{"name":"garm-abc123","provider_id":"mac-mini-01/garm-abc123","state":"running","last_error":"guest agent not up","ip":"192.168.64.5"}`))
	cfg := writeConfig(t, providerConfig(t))
	bootstrap, _ := json.Marshal(commonParams.BootstrapInstance{Name: "garm-abc123"})

	// Act: create.
	code, stdout, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandCreateInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_CONTROLLER_ID":        "controller-1",
	}, string(bootstrap))

	// Assert: the provider reports an error, not a healthy instance.
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr %q), want %d", code, stderr, exitOK)
	}
	var instance commonParams.ProviderInstance
	if err := json.Unmarshal([]byte(stdout), &instance); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if instance.Status != commonParams.InstanceError {
		t.Fatalf("status = %q, want %q", instance.Status, commonParams.InstanceError)
	}
	if !strings.Contains(string(instance.ProviderFault), "guest agent not up") {
		t.Fatalf("provider_fault = %q, want the exec failure", string(instance.ProviderFault))
	}
}

func TestCreateCapacityFaultReturnsErroredInstance(t *testing.T) {
	// Arrange: hostctl create exits 1 with a capacity fault.
	t.Setenv("SSH_STUB_EXIT", "1")
	t.Setenv("SSH_STUB_STDERR", writeStubFile(t, `{"fault":"capacity","error":"no free slot"}`))
	cfg := writeConfig(t, providerConfig(t))
	bootstrap, _ := json.Marshal(commonParams.BootstrapInstance{Name: "garm-abc123"})

	// Act: create.
	code, stdout, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandCreateInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_CONTROLLER_ID":        "controller-1",
	}, string(bootstrap))

	// Assert: an errored ProviderInstance with the capacity message, not a crash.
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr %q), want %d", code, stderr, exitOK)
	}
	var instance commonParams.ProviderInstance
	if err := json.Unmarshal([]byte(stdout), &instance); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if instance.Status != commonParams.InstanceError {
		t.Fatalf("status = %q, want %q", instance.Status, commonParams.InstanceError)
	}
	if !strings.Contains(string(instance.ProviderFault), "no free slot") {
		t.Fatalf("provider_fault = %q, want the capacity message", string(instance.ProviderFault))
	}
}

func TestCreateHostUnreachableReturnsHardError(t *testing.T) {
	// Arrange: the host is unreachable (ssh exit 255).
	t.Setenv("SSH_STUB_EXIT", "255")
	cfg := writeConfig(t, providerConfig(t))
	bootstrap, _ := json.Marshal(commonParams.BootstrapInstance{Name: "garm-abc123"})

	// Act: create.
	code, stdout, stderr := runWith(t, map[string]string{
		"GARM_COMMAND":              CommandCreateInstance,
		"GARM_PROVIDER_CONFIG_FILE": cfg,
		"GARM_CONTROLLER_ID":        "controller-1",
	}, string(bootstrap))

	// Assert: a hard failure, never a success or a healthy instance.
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unreachable") {
		t.Fatalf("stderr = %q, want an unreachable error", stderr)
	}
}

func TestListInstancesReturnsEmptyArray(t *testing.T) {
	// Arrange: a valid config and no instances (fan-out lands in T5).
	// Act: list.
	// Assert: `[]`, not `null`, because GARM unmarshals into a slice.
	cfg := writeConfig(t, providerConfig(t))
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
