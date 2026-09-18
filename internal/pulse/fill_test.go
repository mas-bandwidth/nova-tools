package pulse

// A card may name a LANE, and a lane is a serial queue over one area of the codebase: at
// most one live card per lane at a time. Fill launches the first ready card of a lane and
// holds the rest in order, naming the live card it is held behind. A card with no LANE is
// launched exactly as before; a LANE no row of the lanes file names is refused with the
// remedy. A live card's lane is read from its own card file under --launched.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// laneCap answers a fixed capacity per bench.
type laneCap map[string]int

func (c laneCap) Capacity(bench string) (int, error) { return c[bench], nil }

// laneLauncher records one line per launched card, bench then card.
type laneLauncher struct{ calls []string }

func (l *laneLauncher) Launch(bench, card string) error {
	l.calls = append(l.calls, bench+" "+card)
	return nil
}

// laneFile writes a lanes file: <name>\t<path prefixes>, one lane per line.
func laneFile(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "lanes.tsv")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestFillHoldsAllButOneCardPerLane: two ready cards name one lane; the first launches,
// the second is held in order behind the first and stays in ready.
func TestFillHoldsAllButOneCardPerLane(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	writeCard(t, ready, "card-002.md", "RESULT: CARD-2\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/ cmd/nova-pulse/")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
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
	if got := len(readyCards(launched)); got != 1 {
		t.Fatalf("launched holds %d cards, want 1", got)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Fatalf("ready holds %d cards, want 1 (the held card)", got)
	}
	if !strings.Contains(out.String(), "FILL HELD card=2 lane=pulse live=card-001.md") {
		t.Fatalf("stdout does not hold the second card behind the live first: %q", out.String())
	}
	if strings.Contains(out.String(), "FILL HELD card=1") {
		t.Fatalf("the first card was held, not launched: %q", out.String())
	}
}

// TestFillLaunchesOneCardInEachLane: two ready cards name two lanes; both launch, and
// neither is held because parallelism is what distinct lanes buy.
func TestFillLaunchesOneCardInEachLane(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "LANE: pulse\n")
	writeCard(t, ready, "card-002.md", "LANE: merge\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/", "merge\tcmd/nova-merge/")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 2 {
		t.Fatalf("launcher calls = %d, want 2 (one per lane): %q", len(l.calls), l.calls)
	}
	if strings.Contains(out.String(), "FILL HELD") {
		t.Fatalf("a distinct-lane card was held: %q", out.String())
	}
}

// TestFillLaunchesCardWithoutLane: a card naming no lane is launched exactly as before.
func TestFillLaunchesCardWithoutLane(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\na card with no lane\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
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
	if strings.Contains(out.String(), "FILL HELD") || strings.Contains(errb.String(), "FILL REFUSED") {
		t.Fatalf("a lane-less card was held or refused: out=%q err=%q", out.String(), errb.String())
	}
}

// TestFillRefusesUnknownLane: a LANE no row of the lanes file names is refused with the
// remedy, and the card stays in ready.
func TestFillRefusesUnknownLane(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "LANE: ghost\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 (unknown lane): %q", len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Fatalf("ready holds %d cards, want 1 (refused card stays)", got)
	}
	want := fmt.Sprintf("FILL REFUSED card=1 lane=ghost remedy=%q",
		fmt.Sprintf("add the lane to %s or drop the LANE line", lanes))
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("stderr = %q, want it to carry %q", errb.String(), want)
	}
}

// TestFillReadsLiveLaneFromLaunchedCard: a card already under --launched is live, its lane
// is read from its own card file, and a ready card in that lane is held behind it.
func TestFillReadsLiveLaneFromLaunchedCard(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, launched, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	writeCard(t, ready, "card-002.md", "RESULT: CARD-2\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 (the lane already has a live card): %q", len(l.calls), l.calls)
	}
	if !strings.Contains(out.String(), "FILL HELD card=2 lane=pulse live=card-001.md") {
		t.Fatalf("stdout does not hold behind the live card under --launched: %q", out.String())
	}
}

// TestCardLaneReadsTheLaneLine: a LANE line names the lane and nothing else does.
func TestCardLaneReadsTheLaneLine(t *testing.T) {
	dir := t.TempDir()
	if got := cardLane(writeCard(t, dir, "card-1.md", "RESULT: CARD-1\nLANE: pulse\nrest\n")); got != "pulse" {
		t.Fatalf("cardLane = %q, want pulse", got)
	}
	if got := cardLane(writeCard(t, dir, "card-2.md", "no lane here\n")); got != "" {
		t.Fatalf("cardLane of a lane-less card = %q, want empty", got)
	}
	if got := cardLane(writeCard(t, dir, "card-3.md", "LANES: not a lane\n")); got != "" {
		t.Fatalf("cardLane matched LANES: = %q, want empty", got)
	}
}

// TestLaneTableReadsNames: the lanes file names each lane, comments and blanks aside.
func TestLaneTableReadsNames(t *testing.T) {
	dir := t.TempDir()
	path := laneFile(t, dir, "# an area", "pulse\tinternal/pulse/ cmd/nova-pulse/", "", "merge\tcmd/nova-merge/")
	table := laneTable(path)
	if _, ok := table["pulse"]; !ok {
		t.Fatalf("lane table has no pulse: %v", table)
	}
	if _, ok := table["merge"]; !ok {
		t.Fatalf("lane table has no merge: %v", table)
	}
	if len(table) != 2 {
		t.Fatalf("lane table = %v, want exactly two lanes", table)
	}
}
