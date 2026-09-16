package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// state-round-trips: the counters a restart used to lose -- the next card number, the tick
// count, the refill tick and the gate count -- are one file, and a load after a save is the
// same state, value for value (class A, bug 3: "the loop must be restarted to take any
// change and loses its counters each time").
func TestStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := State{NextCard: 8135, Tick: 42, RefillTick: 7, Gate: 3}
	if err := want.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got != want {
		t.Fatalf("state round trip = %+v, want %+v", got, want)
	}
}

// state-survives-a-restart: a simulated restart -- a fresh zero State, a fresh load from the
// same directory -- reads every counter back, and the next card number carries on rather
// than colliding with a launched card (bug 1).
func TestStateSurvivesASimulatedRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState on a fresh queue: %v", err)
	}
	if s.NextCard != FirstCard {
		t.Fatalf("a fresh queue starts at %d, got %d", FirstCard, s.NextCard)
	}
	for i := 0; i < 5; i++ {
		s.NextCard++
		s.Tick++
	}
	s.Gate = 2
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The restart: nothing of the old process is in hand but the directory.
	restarted, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState after the restart: %v", err)
	}
	if restarted.NextCard != FirstCard+5 || restarted.Tick != 5 || restarted.Gate != 2 {
		t.Fatalf("after the restart = %+v, want NextCard %d, Tick 5, Gate 2", restarted, FirstCard+5)
	}
	restarted.NextCard++
	if err := restarted.Save(dir); err != nil {
		t.Fatalf("Save after the restart: %v", err)
	}
	again, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if again.NextCard != FirstCard+6 {
		t.Fatalf("the number did not carry on: %+v", again)
	}
}

// state-refuses-a-malformed-file: a state file that is not key=value is a refusal naming
// the file and the line, never a silent zero -- a zeroed next_card reissues live numbers.
func TestStateRefusesMalformedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte("next_card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(dir); err == nil {
		t.Fatal("a malformed state file was read as a zero state")
	} else if !strings.Contains(err.Error(), StateFile) || !strings.Contains(err.Error(), "next_card") {
		t.Fatalf("the refusal names neither the file nor the shape: %v", err)
	}
}
