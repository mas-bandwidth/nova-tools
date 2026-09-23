package pulse

// The fill tick's launch path. Since #3251 the fill holds no lane (the dealer does); a
// card's LANE is only recorded on its launched marker. laneTable stays for the `run` road's
// placement guard (placement.go).

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

// TestFillLaunchesCardWithoutLane: a card naming no lane is launched exactly as before.
func TestFillLaunchesCardWithoutLane(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\na card with no lane\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
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

// The dogfood edges of 2026-09-18, red first. A non-author ran fill and cut on real work:
// a launcher that failed left its card live and its lane held forever, a directory of cut
// cards was stepped over by the glob, and one unknown lane reprinted its refusal every
// five minutes.

// failingLauncher fails every launch with one named error.
type failingLauncher struct {
	err   error
	calls int
}

func (l *failingLauncher) Launch(bench, card string) error { l.calls++; return l.err }

// deadCapacity refuses every capacity read, as an unreachable bench does.
type deadCapacity struct{ err error }

func (c deadCapacity) Capacity(bench string) (int, error) { return 0, c.err }

// markersIn lists the marker files beside a card, by suffix kind.
func markersIn(t *testing.T, dir, glob string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, glob))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestFillReturnsAFailedLaunchToReady: an exit-7 launcher is not a card that ran. The card
// goes back to --ready with a .failed-1 marker in the markers directory beside the queue
// (#2013), nothing stays under --launched, the lane is free again, and the tick counts it
// failed=1 launched=0.
func TestFillReturnsAFailedLaunchToReady(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	l := &failingLauncher{err: fmt.Errorf("exit status 7")}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-a"},
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 1 {
		t.Fatalf("fill exit = %d, want 1 (the one bench filled nothing); stderr=%q", code, errb.String())
	}
	if got := len(readyCards(launched)); got != 0 {
		t.Fatalf("launched holds %d cards, want 0: a card that never ran is not live", got)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Fatalf("ready holds %d cards, want 1 (the failed card comes back)", got)
	}
	if got := markersIn(t, ready+"-markers", "card-001.md.failed-*"); len(got) != 1 {
		t.Fatalf("failed markers = %v, want exactly card-001.md.failed-1", got)
	} else if filepath.Base(got[0]) != "card-001.md.failed-1" {
		t.Fatalf("marker = %q, want card-001.md.failed-1", filepath.Base(got[0]))
	}
	line := strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0]
	if want := "FILL tick=1 bench-a:launched=0,failed=1 ready=1"; line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
	if !strings.Contains(errb.String(), "FILL NOTE tick=1") || !strings.Contains(errb.String(), "exit status 7") {
		t.Fatalf("stderr does not carry the launcher's reason: %q", errb.String())
	}
}

// oneFailingLauncher fails the one card it is told to fail and lands the rest.
type oneFailingLauncher struct {
	fail  string
	calls int
	seen  []string
}

func (l *oneFailingLauncher) Launch(bench, card string) error {
	l.calls++
	l.seen = append(l.seen, filepath.Base(card))
	if filepath.Base(card) == l.fail {
		return fmt.Errorf("exit status 7")
	}
	return nil
}

// TestFillRefusesAReadyFileTheGlobWouldSkip: cut wrote 42.md into a ready directory and the
// tick stepped over it in silence. One refusal names the file and the contract.
func TestFillRefusesAReadyFileTheGlobWouldSkip(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "42.md", "a card cut under the old name\n")
	writeCard(t, ready, "card-001.md", "a card\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-a"},
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "FILL REFUSED ready=") || !strings.Contains(errb.String(), "file=42.md") {
		t.Fatalf("stderr does not name the file the glob would skip: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "card-<n>.md") {
		t.Fatalf("the refusal does not name the filename contract: %q", errb.String())
	}
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1 (only the card-<n>.md is a card): %q", len(l.calls), l.calls)
	}
}

// TestFillExitsOneWhenEveryBenchFailed: a tick that reached no bench at all is not a quiet
// success.
func TestFillExitsOneWhenEveryBenchFailed(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-a", "bench-b"},
		Machines: machinesFile(t, dir, []string{"bench-a", "bench-b"}, nil),
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: deadCapacity{err: fmt.Errorf("exit status 255")},
		Launcher: &laneLauncher{},
	})
	if code != 1 {
		t.Fatalf("fill exit = %d, want 1 when every bench failed; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "exit status 255") {
		t.Fatalf("stderr does not carry the capacity reader's reason: %q", errb.String())
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

// TestFillOnlyLaunchesTheCardsItWasGiven: a ready directory is shared with other lines, and
// a fill with no filter launched their cards on its own benches (dogfood, 2026-09-18).
// --only is the whitelist; a card nobody selected stays ready, untouched and unrefused.
func TestFillOnlyLaunchesTheCardsItWasGiven(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-9382.md", "another line's card\n")
	writeCard(t, ready, "card-9601.md", "mine\n")
	writeCard(t, ready, "card-9602.md", "mine too\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Benches:  []string{"bench-a"},
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Only:     []string{"card-960*"},
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
		t.Fatalf("launcher calls = %d, want 2 (only the selected cards): %q", len(l.calls), l.calls)
	}
	for _, call := range l.calls {
		if strings.Contains(call, "card-9382") {
			t.Fatalf("another line's card was launched: %q", call)
		}
	}
	if _, err := os.Stat(filepath.Join(ready, "card-9382.md")); err != nil {
		t.Fatalf("the unselected card did not stay ready: %v", err)
	}
	if strings.Contains(errb.String(), "REFUSED") {
		t.Fatalf("an unselected card was refused rather than left alone: %q", errb.String())
	}
}

// TestSelectedCardsMatchesThreeSpellings: a card is named by its filename, by its name
// without .md, or by its number alone, and a glob stands for any of them.
func TestSelectedCardsMatchesThreeSpellings(t *testing.T) {
	cards := []string{"/q/card-9601.md", "/q/card-9602.md", "/q/card-42.md"}
	for _, c := range []struct {
		pattern string
		want    int
	}{
		{"card-9601.md", 1}, {"card-9601", 1}, {"9601", 1}, {"960*", 2}, {"card-*", 3}, {"nothing", 0},
	} {
		if got := len(selectedCards(cards, []string{c.pattern})); got != c.want {
			t.Errorf("--only %q selected %d cards, want %d", c.pattern, got, c.want)
		}
	}
	if got := len(selectedCards(cards, nil)); got != 3 {
		t.Errorf("no --only selected %d cards, want every one", got)
	}
}
