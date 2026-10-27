package hostctl

import (
	"strings"
	"testing"
)

func TestCanTransitionLegalMoves(t *testing.T) {
	// Arrange: the state machine edges from the architecture decision record.
	cases := []struct {
		from, to State
		want     bool
	}{
		{StateCloning, StateRunning, true},
		{StateRunning, StateReady, true},
		{StateReady, StateBootstrapping, true},
		{StateBootstrapping, StateIdle, true},
		{StateDeleting, StateGone, true},
		{StateCloning, StateReady, false}, // must pass through running
		{StateReady, StateRunning, false}, // never regress
		{StateGone, StateRunning, false},  // nothing revives a gone VM
		{StateFailed, StateReady, false},  // failed is terminal unless deleted
	}
	for _, c := range cases {
		// Act: ask the machine.
		// Assert: the edge matches the model.
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestCanTransitionDeletingAllowedFromAnyLiveState(t *testing.T) {
	// Arrange: every live state may be deleted.
	live := []State{StateCloning, StateRunning, StateReady, StateBootstrapping, StateIdle, StateFailed, StateAdopted, StateQuarantined}
	for _, from := range live {
		// Act / Assert: deleting is always a legal next step.
		if !CanTransition(from, StateDeleting) {
			t.Errorf("CanTransition(%s, deleting) = false, want true", from)
		}
	}
}

func TestIsLiveCountsEverythingButGone(t *testing.T) {
	// Arrange: capacity is honest until a VM is confirmed gone.
	live := []State{StateCloning, StateRunning, StateReady, StateBootstrapping, StateIdle, StateDeleting, StateFailed, StateAdopted, StateQuarantined}
	for _, s := range live {
		if !s.IsLive() {
			t.Errorf("IsLive(%s) = false, want true", s)
		}
	}
	if StateGone.IsLive() {
		t.Error("IsLive(gone) = true, want false")
	}
}

func TestValidName(t *testing.T) {
	// Arrange: names that are safe versus names that could reach a shell or a
	// Tart flag parser.
	valid := []string{"garm-abc123", "a", "macos-runner-01", "x0", "trailing-"}
	invalid := []string{
		"", "-lead", "UPPER", "has space", "a..b",
		"a;rm-rf", "$(id)", "a\nb", strings.Repeat("a", 300),
	}
	for _, name := range valid {
		if !ValidName(name) {
			t.Errorf("ValidName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if ValidName(name) {
			t.Errorf("ValidName(%q) = true, want false", name)
		}
	}
}
