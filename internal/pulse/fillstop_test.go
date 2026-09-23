package pulse

// The STOP file is how a resident fill is taken off the fleet without killing anything:
// touch it, and the tick in flight finishes, no further card is claimed, and every card
// already live on a bench keeps running. Nothing is signalled, nothing is reaped, no lease
// is given back. It is the one control a resident loop needs that a kill cannot give: a
// kill in the middle of a launch leaves a card under --launched that nobody started.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFillStopFileStopsNewLaunchesAndLeavesLiveCardsAlone: the stop file is already there
// when the fill starts. Nothing is claimed out of --ready, the live cards under --launched
// are untouched, one line says which file stopped it, and the exit is 0 -- a stop asked for
// is not a failure.
func TestFillStopFileStopsNewLaunchesAndLeavesLiveCardsAlone(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 4; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	for i := 1; i <= 3; i++ {
		writeCard(t, launched, fmt.Sprintf("card-live-%03d.md", i), "a live card\n")
	}
	stop := filepath.Join(dir, "STOP")
	if err := os.WriteFile(stop, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Stop:     stop,
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0 (a stop asked for is not a failure); stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 under the stop file: %q", len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 4 {
		t.Fatalf("ready holds %d cards, want 4 (nothing was claimed)", got)
	}
	if got := len(readyCards(launched)); got != 3 {
		t.Fatalf("launched holds %d cards, want the 3 live ones, untouched", got)
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{"FILL STOP", "tick=1", "live=3", stop} {
		if !strings.Contains(line, want) {
			t.Fatalf("the stop line does not carry %q: %q", want, line)
		}
	}
}

// TestFillStopFileEndsTheResidentLoopAfterTheTickInFlight: the file appears while the loop
// is resident. The tick that was running finishes its launches -- a card half-claimed is
// the one thing a stop must not leave behind -- and the next tick launches nothing and
// returns.
func TestFillStopFileEndsTheResidentLoopAfterTheTickInFlight(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 6; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	stop := filepath.Join(dir, "STOP")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Stop:     stop,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 2},
		Launcher: l,
		Sleep: func(time.Duration) {
			if err := os.WriteFile(stop, nil, 0o644); err != nil {
				t.Error(err)
			}
		},
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 2 {
		t.Fatalf("launcher calls = %d, want 2 (tick 1 finished, tick 2 launched nothing): %q",
			len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 4 {
		t.Fatalf("ready holds %d cards, want 4", got)
	}
	if !strings.Contains(out.String(), "FILL tick=1 bench-a:launched=2,failed=0") {
		t.Fatalf("the tick in flight did not finish: %q", out.String())
	}
	if !strings.Contains(out.String(), "FILL STOP tick=2") {
		t.Fatalf("the loop did not stop on the next tick: %q", out.String())
	}
}

// TestFillWithNoStopFileNamedIsTheLoopItAlwaysWas: no --stop is no stop check at all, and a
// --stop naming a file that is not there is a loop that runs.
func TestFillWithNoStopFileNamedIsTheLoopItAlwaysWas(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Stop:     filepath.Join(dir, "never-written"),
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1: %q", len(l.calls), l.calls)
	}
	if strings.Contains(out.String(), "FILL STOP") {
		t.Fatalf("a stop file that is not there stopped the loop: %q", out.String())
	}
}
