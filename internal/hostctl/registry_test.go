package hostctl

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryCorruptRecordIsQuarantinedNotFatal(t *testing.T) {
	// Arrange: a record file that is truncated JSON.
	root := t.TempDir()
	reg := NewRegistry(root)
	if err := os.MkdirAll(filepath.Join(root, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "instances", "garm-abc123.json")
	if err := os.WriteFile(path, []byte(`{"name":"garm-abc`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act: load it.
	_, err := reg.Load("garm-abc123")

	// Assert: treated as absent (never fatal) and the file is moved aside.
	if err != ErrNotFound {
		t.Fatalf("Load error = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(path + ".corrupt"); statErr != nil {
		t.Fatalf("corrupt file not quarantined: %v", statErr)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("original corrupt file still present: %v", statErr)
	}
}

func TestRegistryMissingDirectoryYieldsEmptyList(t *testing.T) {
	// Arrange: no instances directory at all.
	reg := NewRegistry(t.TempDir())

	// Act: list.
	records, err := reg.All()

	// Assert: empty, no error.
	if err != nil {
		t.Fatalf("All error = %v, want nil", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %d, want 0", len(records))
	}
}

func TestRegistryReadOnlyDiskFailsCleanly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod cannot make the directory read-only")
	}
	// Arrange: a read-only instances directory.
	root := t.TempDir()
	instDir := filepath.Join(root, "instances")
	if err := os.MkdirAll(instDir, 0o500); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root)
	rec := Record{Name: "garm-abc123", State: StateReady, CreatedAt: time.Now()}

	// Act: save.
	err := reg.Save(rec)

	// Assert: a clean error, not a panic.
	if err == nil {
		t.Fatal("Save on read-only disk succeeded, want error")
	}
}

func TestRegistryAtomicWrite(t *testing.T) {
	// Arrange: a registry and a record.
	root := t.TempDir()
	reg := NewRegistry(root)
	rec := Record{Name: "garm-abc123", State: StateRunning, CreatedAt: time.Now()}

	// Act: save twice.
	if err := reg.Save(rec); err != nil {
		t.Fatal(err)
	}
	rec.State = StateReady
	if err := reg.Save(rec); err != nil {
		t.Fatal(err)
	}

	// Assert: only the final record exists, no temp file left behind.
	got, err := reg.Load("garm-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateReady {
		t.Fatalf("state = %s, want ready", got.State)
	}
	tmp := filepath.Join(root, "instances", "garm-abc123.json.tmp")
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}
