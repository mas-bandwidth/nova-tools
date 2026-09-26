package control_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/control"
)

func TestValidateState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	exp := now.Add(10 * time.Second)

	// Valid state
	st := control.State{
		Generation: 1,
		Desired:    control.DesiredRun,
		Scope:      control.ScopeFleet,
		By:         "test",
		At:         now,
		Expires:    &exp,
	}
	if err := control.ValidateState(st); err != nil {
		t.Fatalf("valid state failed validation: %v", err)
	}

	// Non-positive generation
	invalidGen := st
	invalidGen.Generation = 0
	if err := control.ValidateState(invalidGen); err == nil {
		t.Fatal("expected error for generation 0")
	}

	// Invalid desired
	invalidDesired := st
	invalidDesired.Desired = "UNKNOWN"
	if err := control.ValidateState(invalidDesired); err == nil {
		t.Fatal("expected error for desired UNKNOWN")
	}

	// Invalid scope
	invalidScope := st
	invalidScope.Scope = "local"
	if err := control.ValidateState(invalidScope); err == nil {
		t.Fatal("expected error for scope local")
	}

	// Empty By
	emptyBy := st
	emptyBy.By = "   "
	if err := control.ValidateState(emptyBy); err == nil {
		t.Fatal("expected error for empty By")
	}

	// Zero At
	zeroAt := st
	zeroAt.At = time.Time{}
	if err := control.ValidateState(zeroAt); err == nil {
		t.Fatal("expected error for zero At")
	}

	// RUN missing Expires
	noExp := st
	noExp.Expires = nil
	if err := control.ValidateState(noExp); err == nil {
		t.Fatal("expected error for RUN missing Expires")
	}

	// PAUSE without Expires is valid
	pauseSt := control.State{
		Generation: 2,
		Desired:    control.DesiredPause,
		Scope:      control.ScopeFleet,
		By:         "test",
		At:         now,
	}
	if err := control.ValidateState(pauseSt); err != nil {
		t.Fatalf("PAUSE state failed validation: %v", err)
	}

	// Non-RUN states must not carry expiry
	pauseExp := pauseSt
	pauseExp.Expires = &exp
	if err := control.ValidateState(pauseExp); err == nil {
		t.Fatal("expected error for PAUSE with expires")
	}
}

func TestValidateUpdate_NonRUNMustNotExpire(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	exp := now.Add(10 * time.Second)
	next := control.State{
		Generation: 1,
		Desired:    control.DesiredStop,
		Scope:      control.ScopeFleet,
		By:         "test",
		At:         now,
		Expires:    &exp,
	}
	if err := control.ValidateUpdate(0, 0, next, now, 30*time.Second); err == nil {
		t.Fatal("expected error for STOP with expires")
	}
}

func TestValidateAck(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	// Valid ack
	ack := control.Ack{
		Generation: 1,
		Owner:      "mac-studio",
		Bench:      "mac-studio",
		Desired:    control.DesiredRun,
		Observed:   now,
	}
	if err := control.ValidateAck(ack); err != nil {
		t.Fatalf("valid ack failed validation: %v", err)
	}

	// Generation <= 0
	badGen := ack
	badGen.Generation = 0
	if err := control.ValidateAck(badGen); err == nil {
		t.Fatal("expected error for generation 0 in ack")
	}

	// Empty owner
	badOwner := ack
	badOwner.Owner = ""
	if err := control.ValidateAck(badOwner); err == nil {
		t.Fatal("expected error for empty owner in ack")
	}

	// Path traversal in owner
	badChars := []string{"../evil", "foo/bar", "foo\\bar", "foo:bar", "foo*bar", "foo?bar"}
	for _, bad := range badChars {
		traversal := ack
		traversal.Owner = bad
		if err := control.ValidateAck(traversal); err == nil {
			t.Fatalf("expected error for invalid owner characters %q, got nil", bad)
		}
	}

	// Empty bench
	badBench := ack
	badBench.Bench = "   "
	if err := control.ValidateAck(badBench); err == nil {
		t.Fatal("expected error for empty bench in ack")
	}

	// Invalid desired
	badDesired := ack
	badDesired.Desired = "JUNK"
	if err := control.ValidateAck(badDesired); err == nil {
		t.Fatal("expected error for invalid desired in ack")
	}

	// Zero observed
	badObserved := ack
	badObserved.Observed = time.Time{}
	if err := control.ValidateAck(badObserved); err == nil {
		t.Fatal("expected error for zero observed timestamp")
	}
}
