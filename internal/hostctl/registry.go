package hostctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNotFound reports that the registry has no record for a name. A corrupt
// record is quarantined and also surfaces as ErrNotFound, so callers treat it
// as absent rather than failing the verb.
var ErrNotFound = errors.New("instance not found")

// defaultLeaseTTLSeconds is the lease lifetime written to a record when it is
// created or adopted. It keeps the reservation visible across a crashed
// provider; the host registry is the source of truth, not the lease expiry.
const defaultLeaseTTLSeconds = 900

// Lease records who currently owns a record and for how long.
type Lease struct {
	Holder     string    `json:"holder"`
	Epoch      int       `json:"epoch"`
	AcquiredAt time.Time `json:"acquired_at"`
	TTLSec     int       `json:"ttl_s"`
}

// Record is the authoritative per-instance document on one host, stored at
// /var/db/tart-fleet/instances/<name>.json. It is the source of truth for which
// VMs this Mac runs and which licence slot each one occupies.
type Record struct {
	Name          string     `json:"name"`
	ProviderID    string     `json:"provider_id"`
	ControllerID  string     `json:"controller_id"`
	OwnerID       string     `json:"owner_id"`
	Image         string     `json:"image"`
	State         State      `json:"state"`
	Lease         Lease      `json:"lease"`
	BootAttempts  int        `json:"boot_attempts"`
	LastError     string     `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	QuarantinedAt *time.Time `json:"quarantined_at,omitempty"`
}

// Registry persists records under root/instances/<name>.json with atomic
// replace and 0600 permissions. It performs no locking itself: the Service
// holds the host flock for the whole verb, and a Registry is only ever touched
// while that lock is held.
type Registry struct {
	instancesDir string
}

// NewRegistry returns a Registry rooted at the given fleet directory.
func NewRegistry(root string) *Registry {
	return &Registry{instancesDir: filepath.Join(root, "instances")}
}

func (r *Registry) path(name string) string {
	return filepath.Join(r.instancesDir, name+".json")
}

// Load returns the record for name. A missing file returns ErrNotFound. A file
// that does not parse is quarantined (renamed to <name>.json.corrupt) and also
// returns ErrNotFound: corruption is never fatal, and the slot it claimed is
// released so the registry keeps describing reality.
func (r *Registry) Load(name string) (Record, error) {
	path := r.path(name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("read record %q: %w", name, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		_ = os.Rename(path, path+".corrupt")
		return Record{}, ErrNotFound
	}
	return rec, nil
}

// Save writes rec atomically: the JSON is written to a sibling temp file with
// 0600 permissions and renamed over the target, so a crash or SIGKILL mid-write
// leaves either the old or the new file, never a truncated one.
func (r *Registry) Save(rec Record) error {
	if err := os.MkdirAll(r.instancesDir, 0o700); err != nil {
		return fmt.Errorf("create instances dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode record %q: %w", rec.Name, err)
	}
	data = append(data, '\n')
	tmp := r.path(rec.Name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write record %q: %w", rec.Name, err)
	}
	if err := os.Rename(tmp, r.path(rec.Name)); err != nil {
		return fmt.Errorf("replace record %q: %w", rec.Name, err)
	}
	return nil
}

// Delete removes the record for name. A missing record is a no-op.
func (r *Registry) Delete(name string) error {
	err := os.Remove(r.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// All returns every non-corrupt record, sorted by name. Missing directories
// yield an empty result; corrupt files are quarantined by Load and skipped.
func (r *Registry) All() ([]Record, error) {
	entries, err := os.ReadDir(r.instancesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read instances dir: %w", err)
	}
	records := make([]Record, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		rec, err := r.Load(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, nil
}
