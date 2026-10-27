package hostctl

import (
	"os"
)

// Reconcile brings the registry into line with `tart list`. It runs at startup
// and on every status (and before a create reserves capacity). It:
//
//   - reaps quarantined VMs whose quarantine TTL has elapsed;
//   - drops records whose VM has vanished from tart list (someone ran
//     `tart delete` by hand) — except deleting tombstones, which the delete
//     verb itself completes;
//   - adopts unrecorded VMs that carry this controller's label (they keep a
//     lease and count against capacity);
//   - quarantines unrecorded VMs that carry no label, for the quarantine TTL;
//   - leaves alone unrecorded VMs that carry a foreign controller's label.
//
// A provider killed between `tart clone` and the record write leaves a VM with
// no record here: the next Reconcile adopts or quarantines it, so it is never
// silently orphaned.
func (s *Service) Reconcile() error {
	vms, err := s.tart.List()
	if err != nil {
		return err
	}
	byName := make(map[string]VM, len(vms))
	for _, vm := range vms {
		byName[vm.Name] = vm
	}

	records, err := s.reg.All()
	if err != nil {
		return err
	}
	recByName := make(map[string]Record, len(records))
	for _, rec := range records {
		recByName[rec.Name] = rec
	}

	now := s.now()

	// 1. Reap quarantined VMs whose TTL has elapsed. A failed delete keeps the
	// record so the next reconcile retries; a lying delete that leaves the VM
	// behind causes it to be re-quarantined next time.
	for _, rec := range records {
		if rec.State != StateQuarantined || rec.QuarantinedAt == nil {
			continue
		}
		if now.Sub(*rec.QuarantinedAt) < s.quarantineTTL {
			continue
		}
		if err := s.tart.Delete(rec.Name); err != nil {
			continue
		}
		_ = s.reg.Delete(rec.Name)
	}

	// 2. Drop records whose VM is gone and that are not deleting tombstones.
	for _, rec := range records {
		if _, ok := byName[rec.Name]; ok {
			continue
		}
		if rec.State == StateDeleting {
			continue
		}
		_ = s.reg.Delete(rec.Name)
	}

	// 3. Adopt or quarantine unrecorded VMs.
	for _, vm := range vms {
		if _, ok := recByName[vm.Name]; ok {
			continue
		}
		switch label := vm.Label(controllerLabelKey); {
		case label == s.controllerID:
			rec := Record{
				Name:         vm.Name,
				ProviderID:   s.hostName + "/" + vm.Name,
				ControllerID: s.controllerID,
				State:        StateAdopted,
				Lease: Lease{
					Holder:     s.controllerID,
					AcquiredAt: now,
					TTLSec:     defaultLeaseTTLSeconds,
				},
				CreatedAt: now,
				UpdatedAt: now,
			}
			_ = s.reg.Save(rec)
		case label == "":
			quarantinedAt := now
			rec := Record{
				Name:          vm.Name,
				State:         StateQuarantined,
				QuarantinedAt: &quarantinedAt,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			_ = s.reg.Save(rec)
		default:
			// A foreign controller owns this VM: leave it entirely alone.
		}
	}
	return nil
}

// hostname returns the machine's host name, used as the fallback host component
// of provider IDs when no TART_FLEET_HOST override is configured.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown-host"
	}
	return h
}
