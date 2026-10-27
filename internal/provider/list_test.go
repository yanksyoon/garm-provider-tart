package provider

import (
	"encoding/json"
	"strings"
	"testing"

	commonParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

func instanceWith(t *testing.T, providerID, name string, state hostctl.State) hostctl.Instance {
	t.Helper()
	return hostctl.Instance{
		Record: hostctl.Record{
			Name:         name,
			ProviderID:   providerID,
			ControllerID: "controller-1",
			OwnerID:      "pool-1",
			State:        state,
		},
		IP:     "192.168.64.5",
		InTart: true,
	}
}

func TestListInstancesMergesAndDeduplicates(t *testing.T) {
	// Arrange: host A reports two VMs, host B reports one plus a duplicate
	// provider id (a stale/corrupt read of the same VM).
	a1 := instanceWith(t, "mac-mini-01/vm-1", "vm-1", hostctl.StateReady)
	a2 := instanceWith(t, "mac-mini-01/vm-2", "vm-2", hostctl.StateReady)
	dup := instanceWith(t, "mac-mini-01/vm-1", "vm-1", hostctl.StateReady) // same provider id
	b1 := instanceWith(t, "mac-mini-02/vm-3", "vm-3", hostctl.StateReady)
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb != "list" {
			return nil, nil
		}
		switch host {
		case "mac-mini-01":
			return listJSON(t, a1, a2), nil
		case "mac-mini-02":
			return listJSON(t, dup, b1), nil
		}
		return listJSON(t), nil
	}
	p, _, _ := newFleetProvider(t, []Host{
		{Name: "mac-mini-01", Address: "10.20.0.11"},
		{Name: "mac-mini-02", Address: "10.20.0.12"},
	}, handler)
	var out strings.Builder

	// Act: list.
	if err := p.ListInstances("pool-1", &out); err != nil {
		t.Fatalf("ListInstances error = %v", err)
	}

	// Assert: three unique instances (the duplicate collapsed), never null.
	var instances []commonParams.ProviderInstance
	if err := json.Unmarshal([]byte(out.String()), &instances); err != nil {
		t.Fatalf("list output not a JSON array: %v (%q)", err, out.String())
	}
	if len(instances) != 3 {
		t.Fatalf("instances = %d, want 3 (deduplicated)", len(instances))
	}
}

func TestListInstancesSkipsUnknownHost(t *testing.T) {
	// Arrange: host A reachable, host B unreachable.
	a1 := instanceWith(t, "mac-mini-01/vm-1", "vm-1", hostctl.StateReady)
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb != "list" {
			return nil, nil
		}
		if host == "mac-mini-02" {
			return nil, ErrHostUnreachable
		}
		return listJSON(t, a1), nil
	}
	p, _, stderr := newFleetProvider(t, []Host{
		{Name: "mac-mini-01", Address: "10.20.0.11"},
		{Name: "mac-mini-02", Address: "10.20.0.12"},
	}, handler)
	var out strings.Builder

	// Act: list.
	if err := p.ListInstances("pool-1", &out); err != nil {
		t.Fatalf("ListInstances error = %v", err)
	}

	// Assert: A's VM returned, B skipped with a warning, result not null.
	var instances []commonParams.ProviderInstance
	if err := json.Unmarshal([]byte(out.String()), &instances); err != nil {
		t.Fatalf("list output not a JSON array: %v", err)
	}
	if len(instances) != 1 || instances[0].ProviderID != "mac-mini-01/vm-1" {
		t.Fatalf("instances = %+v, want just mac-mini-01/vm-1", instances)
	}
	if !strings.Contains(stderr.String(), "mac-mini-02") {
		t.Fatalf("stderr = %q, want a warning naming mac-mini-02", stderr.String())
	}
}

func TestListInstancesEmptyReturnsArray(t *testing.T) {
	// Arrange: no VMs anywhere.
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb == "list" {
			return listJSON(t), nil
		}
		return nil, nil
	}
	p, _, _ := newFleetProvider(t, threeHosts(), handler)
	var out strings.Builder

	// Act: list.
	if err := p.ListInstances("pool-1", &out); err != nil {
		t.Fatalf("ListInstances error = %v", err)
	}

	// Assert: `[]`, never `null`.
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("output = %q, want []", out.String())
	}
}

func TestListInstancesPassesControllerAndOwnerFilter(t *testing.T) {
	// Arrange: capture the list verb args.
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb == "list" {
			return listJSON(t), nil
		}
		return nil, nil
	}
	p, f, _ := newFleetProvider(t, []Host{{Name: "mac-mini-01", Address: "10.20.0.11"}}, handler)
	var out strings.Builder

	// Act: list with an opaque pool id (a synthetic scale-set id, not a UUID).
	if err := p.ListInstances("scale-set-synthetic-id", &out); err != nil {
		t.Fatalf("ListInstances error = %v", err)
	}

	// Assert: the opaque id is passed verbatim as --owner, with the controller.
	calls := f.callsFor("mac-mini-01", "list")
	if len(calls) != 1 {
		t.Fatalf("list calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "--controller controller-1") {
		t.Fatalf("args %q missing --controller controller-1", calls[0])
	}
	if !strings.Contains(calls[0], "--owner scale-set-synthetic-id") {
		t.Fatalf("args %q missing --owner scale-set-synthetic-id", calls[0])
	}
}

func TestListInstancesRequiresPoolID(t *testing.T) {
	// Arrange: no GARM_POOL_ID.
	p, _, _ := newFleetProvider(t, threeHosts(), nil)
	var out strings.Builder

	// Act: list with an empty pool id.
	err := p.ListInstances("", &out)

	// Assert: a hard error, not an empty fleet.
	if err == nil {
		t.Fatal("ListInstances with empty pool id returned nil, want error")
	}
}

func TestListInstancesRestartSafeReconstructsFleet(t *testing.T) {
	// Arrange: the hosts know about two VMs; the provider holds no local state.
	handler := func(host, verb string, args []string) ([]byte, error) {
		if verb != "list" {
			return nil, nil
		}
		if host == "mac-mini-01" {
			return listJSON(t, instanceWith(t, "mac-mini-01/vm-1", "vm-1", hostctl.StateReady)), nil
		}
		return listJSON(t, instanceWith(t, "mac-mini-02/vm-2", "vm-2", hostctl.StateReady)), nil
	}
	hosts := []Host{
		{Name: "mac-mini-01", Address: "10.20.0.11"},
		{Name: "mac-mini-02", Address: "10.20.0.12"},
	}

	listCount := func() int {
		p, _, _ := newFleetProvider(t, hosts, handler)
		var out strings.Builder
		if err := p.ListInstances("pool-1", &out); err != nil {
			t.Fatalf("ListInstances error = %v", err)
		}
		var instances []commonParams.ProviderInstance
		if err := json.Unmarshal([]byte(out.String()), &instances); err != nil {
			t.Fatalf("list output not a JSON array: %v", err)
		}
		return len(instances)
	}

	// Act / Assert: a freshly constructed provider (a "restart" with zero local
	// state) returns the same fleet the hosts see, twice.
	if got := listCount(); got != 2 {
		t.Fatalf("first list = %d instances, want 2", got)
	}
	if got := listCount(); got != 2 {
		t.Fatalf("second list = %d instances, want 2", got)
	}
}
