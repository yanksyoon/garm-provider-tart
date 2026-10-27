package hostctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tartStub returns the testdata/bin/tart stub path.
func tartStub(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "bin", "tart"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("tart stub missing: %v", err)
	}
	return abs
}

func TestRealTartCloneUsesStacked(t *testing.T) {
	// Arrange: a RealTart pointed at the stub, with an argv log.
	logPath := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("TART_STUB_LOG", logPath)
	rt := NewRealTart(tartStub(t))

	// Act: clone.
	if err := rt.Clone("ghcr.io/x/macos@sha256:abc", "garm-abc123"); err != nil {
		t.Fatal(err)
	}

	// Assert: the argv carries --stacked, the image, then the name.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "clone --stacked ghcr.io/x/macos@sha256:abc garm-abc123") {
		t.Fatalf("clone argv = %q, want stacked clone", string(data))
	}
}

func TestRealTartListParsesLabels(t *testing.T) {
	// Arrange: the stub replays a list with labels.
	t.Setenv("TART_STUB_LIST", `[{"name":"garm-abc123","state":"running","labels":{"garm-controller-id":"controller-1"}}]`)
	rt := NewRealTart(tartStub(t))

	// Act: list.
	vms, err := rt.List()

	// Assert: the VM and its label come back parsed.
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 {
		t.Fatalf("vms = %d, want 1", len(vms))
	}
	if vms[0].Label("garm-controller-id") != "controller-1" {
		t.Fatalf("label = %q, want controller-1", vms[0].Label("garm-controller-id"))
	}
}

func TestRealTartRunUsesNoGraphics(t *testing.T) {
	// Arrange.
	logPath := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("TART_STUB_LOG", logPath)
	rt := NewRealTart(tartStub(t))

	// Act: run.
	if err := rt.Run("garm-abc123"); err != nil {
		t.Fatal(err)
	}

	// Assert: headless.
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "run --no-graphics garm-abc123") {
		t.Fatalf("run argv = %q, want --no-graphics", string(data))
	}
}
