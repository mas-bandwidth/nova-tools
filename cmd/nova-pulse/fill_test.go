package main

// fill-loop.sh's tick body as a verb (#1142): every tick, for each bench, read the
// bench's capacity, cap it, pop that many card-*.md from --ready in filename order,
// move each to --launched and launch it. The seams -- capacity and the per-card
// launcher -- are injected, so no test opens an ssh connection or spawns a process.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// fillCapStub answers a fixed capacity per bench.
type fillCapStub map[string]int

func (f fillCapStub) Capacity(bench string) (int, error) { return f[bench], nil }

// fillLaunchStub records one line per launched card, bench then card.
type fillLaunchStub struct{ calls []string }

func (l *fillLaunchStub) Launch(bench, card string) error {
	l.calls = append(l.calls, bench+" "+card)
	return nil
}

func fillReady(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		writeMainFile(t, dir, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
}

func fillCount(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "card-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

// TestFillLaunchesCapacityPerBench: bench-a takes 3, bench-b takes 2, the rest stay ready.
// The FILL line names every bench and the remaining ready count.
func TestFillLaunchesCapacityPerBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 8)
	cap := fillCapStub{"bench-a": 3, "bench-b": 2}
	l := &fillLaunchStub{}
	var out, errb bytes.Buffer
	code := pulse.Fill(pulse.FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-a", "bench-b"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: cap,
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 5 {
		t.Fatalf("launcher calls = %d, want 5: %q", len(l.calls), l.calls)
	}
	// Oldest first, bench-a before bench-b.
	wantFirst := "bench-a " + filepath.Join(launched, "card-001.md")
	if l.calls[0] != wantFirst {
		t.Fatalf("first launch = %q, want %q", l.calls[0], wantFirst)
	}
	if got := fillCount(t, launched); got != 5 {
		t.Fatalf("launched holds %d cards, want 5", got)
	}
	if got := fillCount(t, ready); got != 3 {
		t.Fatalf("ready holds %d cards, want 3", got)
	}
	line := strings.TrimSpace(out.String())
	want := "FILL tick=1 bench-a:launched=3 bench-b:launched=2 ready=3"
	if line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
}

// TestFillCapsAtThirty: a bench whose formula allows one hundred cards never takes more
// than thirty in a tick -- the CI reserve fill-loop.sh holds back.
func TestFillCapsAtThirty(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 40)
	l := &fillLaunchStub{}
	var out, errb bytes.Buffer
	code := pulse.Fill(pulse.FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-x"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: fillCapStub{"bench-x": 100},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 30 {
		t.Fatalf("launcher calls = %d, want 30 (the cap)", len(l.calls))
	}
	line := strings.TrimSpace(out.String())
	want := "FILL tick=1 bench-x:launched=30 ready=10"
	if line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
}

// TestFillNeverLaunchesACardTwice: the move out of ready is the claim; a second tick sees
// an empty ready and launches nothing.
func TestFillNeverLaunchesACardTwice(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 4)
	l := &fillLaunchStub{}
	in := pulse.FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-a"},
		Once:     true,
		Capacity: fillCapStub{"bench-a": 100},
		Launcher: l,
	}
	var out1, errb1 bytes.Buffer
	in.Stdout, in.Stderr = &out1, &errb1
	if code := pulse.Fill(in); code != 0 {
		t.Fatalf("first fill exit = %d, stderr=%q", code, errb1.String())
	}
	var out2, errb2 bytes.Buffer
	in.Stdout, in.Stderr = &out2, &errb2
	if code := pulse.Fill(in); code != 0 {
		t.Fatalf("second fill exit = %d, stderr=%q", code, errb2.String())
	}
	if len(l.calls) != 4 {
		t.Fatalf("launcher calls = %d, want 4 (no card launched twice): %q", len(l.calls), l.calls)
	}
	want := "FILL tick=1 bench-a:launched=0 ready=0"
	if line := strings.TrimSpace(out2.String()); line != want {
		t.Fatalf("second FILL line = %q, want %q", line, want)
	}
}

// TestFillRefusesWithoutReadyAndLaunched: both directories are flags, so neither is guessed.
func TestFillRefusesWithoutReadyAndLaunched(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"fill"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("fill with no flags exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--ready is required") {
		t.Fatalf("refusal does not name --ready: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "--launched is required") {
		t.Fatalf("refusal does not name --launched: %q", errb.String())
	}
}
