package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

func TestRemoveAllDeletesOwnControllerVMs(t *testing.T) {
	// Arrange: one own VM per host.
	ownA := instanceWith(t, "mac-mini-01/own-a", "own-a", hostctl.StateReady)
	ownB := instanceWith(t, "mac-mini-02/own-b", "own-b", hostctl.StateReady)
	handler := func(host, verb string, args []string) ([]byte, error) {
		switch verb {
		case "list":
			if host == "mac-mini-01" {
				return listJSON(t, ownA), nil
			}
			return listJSON(t, ownB), nil
		case "delete":
			return nil, nil
		}
		return nil, nil
	}
	p, f, stderr := newFleetProvider(t, []Host{
		{Name: "mac-mini-01", Address: "10.20.0.11"},
		{Name: "mac-mini-02", Address: "10.20.0.12"},
	}, handler)

	// Act: remove all.
	if err := p.RemoveAllInstances(); err != nil {
		t.Fatalf("RemoveAllInstances error = %v, want nil", err)
	}

	// Assert: each own VM deleted, on its own host, and reaped counted.
	if got := f.callsFor("mac-mini-01", "delete"); len(got) != 1 || !strings.Contains(got[0], "own-a") {
		t.Fatalf("delete on mac-mini-01 = %v, want own-a", got)
	}
	if got := f.callsFor("mac-mini-02", "delete"); len(got) != 1 || !strings.Contains(got[0], "own-b") {
		t.Fatalf("delete on mac-mini-02 = %v, want own-b", got)
	}
	if !strings.Contains(stderr.String(), "orphans_reaped=2") {
		t.Fatalf("stderr = %q, want orphans_reaped=2", stderr.String())
	}
}

func TestRemoveAllReportsUnreachableHost(t *testing.T) {
	// Arrange: host A unreachable, host B has one own VM.
	ownB := instanceWith(t, "mac-mini-02/own-b", "own-b", hostctl.StateReady)
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb == "list" {
			if host == "mac-mini-01" {
				return nil, ErrHostUnreachable
			}
			return listJSON(t, ownB), nil
		}
		if verb == "delete" {
			return nil, nil
		}
		return nil, nil
	}
	p, f, stderr := newFleetProvider(t, []Host{
		{Name: "mac-mini-01", Address: "10.20.0.11"},
		{Name: "mac-mini-02", Address: "10.20.0.12"},
	}, handler)

	// Act: remove all.
	if err := p.RemoveAllInstances(); err != nil {
		t.Fatalf("RemoveAllInstances error = %v, want nil", err)
	}

	// Assert: B's VM deleted, A reported as skipped, reaped=1.
	if got := f.callsFor("mac-mini-02", "delete"); len(got) != 1 {
		t.Fatalf("delete on mac-mini-02 = %v, want 1", got)
	}
	if !strings.Contains(stderr.String(), "mac-mini-01") {
		t.Fatalf("stderr = %q, want a skip warning naming mac-mini-01", stderr.String())
	}
	if !strings.Contains(stderr.String(), "orphans_reaped=1") {
		t.Fatalf("stderr = %q, want orphans_reaped=1", stderr.String())
	}
}

func TestRemoveAllDoesNotTouchForeignVMs(t *testing.T) {
	// Arrange: the host-side list filter returns only this controller's VM; the
	// foreign VM is never handed to the provider.
	own := instanceWith(t, "mac-mini-01/own-a", "own-a", hostctl.StateReady)
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb == "list" {
			// hostctl `list --controller controller-1` excludes foreign VMs.
			return listJSON(t, own), nil
		}
		if verb == "delete" {
			return nil, nil
		}
		return nil, nil
	}
	p, f, _ := newFleetProvider(t, []Host{{Name: "mac-mini-01", Address: "10.20.0.11"}}, handler)

	// Act: remove all.
	if err := p.RemoveAllInstances(); err != nil {
		t.Fatalf("RemoveAllInstances error = %v, want nil", err)
	}

	// Assert: only the own VM is ever named in a delete.
	for _, call := range f.callsFor("mac-mini-01", "delete") {
		if strings.Contains(call, "foreign-vm") {
			t.Fatalf("delete args %q touch a foreign VM", call)
		}
	}
	if got := f.callsFor("mac-mini-01", "delete"); len(got) != 1 || !strings.Contains(got[0], "own-a") {
		t.Fatalf("delete calls = %v, want exactly own-a", got)
	}
}

func TestRemoveAllReportsDeleteFailure(t *testing.T) {
	// Arrange: the VM is listed but the delete fails.
	own := instanceWith(t, "mac-mini-01/own-a", "own-a", hostctl.StateReady)
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb == "list" {
			return listJSON(t, own), nil
		}
		if verb == "delete" {
			return nil, errors.New("tart delete: device busy")
		}
		return nil, nil
	}
	p, _, stderr := newFleetProvider(t, []Host{{Name: "mac-mini-01", Address: "10.20.0.11"}}, handler)

	// Act: remove all.
	if err := p.RemoveAllInstances(); err != nil {
		t.Fatalf("RemoveAllInstances error = %v, want nil", err)
	}

	// Assert: the failure is reported, not swallowed, and reaped stays 0.
	if !strings.Contains(stderr.String(), "own-a") {
		t.Fatalf("stderr = %q, want a report naming own-a", stderr.String())
	}
	if !strings.Contains(stderr.String(), "orphans_reaped=0") {
		t.Fatalf("stderr = %q, want orphans_reaped=0", stderr.String())
	}
}
