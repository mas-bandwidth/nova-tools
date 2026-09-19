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
	"time"
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
	if !strings.Contains(out.String(), "FILL HELD card=card-002.md lane=pulse live=card-001.md") {
		t.Fatalf("stdout does not hold the second card behind the live first: %q", out.String())
	}
	if strings.Contains(out.String(), "FILL HELD card=card-001.md") {
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
	want := fmt.Sprintf("FILL REFUSED card=card-001.md lane=ghost remedy=%q",
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
	if !strings.Contains(out.String(), "FILL HELD card=card-002.md lane=pulse live=card-001.md") {
		t.Fatalf("stdout does not hold behind the live card under --launched: %q", out.String())
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
// goes back to --ready with a .failed-1 marker, nothing stays under --launched, the lane is
// free again, and the tick counts it failed=1 launched=0.
func TestFillReturnsAFailedLaunchToReady(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	l := &failingLauncher{err: fmt.Errorf("exit status 7")}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
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
	if got := markersIn(t, ready, "card-001.md.failed-*"); len(got) != 1 {
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

// TestFillReleasesTheLaneOfAFailedLaunch: the lane of a card whose launch failed is free in
// the same tick, so the next card in that lane is launched rather than held behind a card
// that never ran.
func TestFillReleasesTheLaneOfAFailedLaunch(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "LANE: pulse\n")
	writeCard(t, ready, "card-002.md", "LANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	l := &oneFailingLauncher{fail: "card-001.md"}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Benches:  []string{"bench-a"},
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0 (one launch landed); stderr=%q", code, errb.String())
	}
	if l.calls != 2 {
		t.Fatalf("launcher calls = %d, want 2 (the lane was released): %q", l.calls, l.seen)
	}
	if strings.Contains(out.String(), "FILL HELD") {
		t.Fatalf("the second card was held behind a card that never ran: %q", out.String())
	}
	line := strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0]
	if want := "FILL tick=1 bench-a:launched=1,failed=1 ready=1"; line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
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

// TestFillRefusesAnUnknownLaneOncePerLanesFile: the refusal is printed once and then
// remembered by a marker; editing the lanes file makes it speak again, because the answer
// may have changed.
func TestFillRefusesAnUnknownLaneOncePerLanesFile(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "LANE: ghost\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	in := FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Benches:  []string{"bench-a"},
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Once:     true,
		Capacity: laneCap{"bench-a": 10},
		Launcher: &laneLauncher{},
	}
	var out1, err1 bytes.Buffer
	in.Stdout, in.Stderr = &out1, &err1
	if code := Fill(in); code != 0 {
		t.Fatalf("first tick exit = %d, stderr=%q", code, err1.String())
	}
	if !strings.Contains(err1.String(), "FILL REFUSED card=card-001.md lane=ghost") {
		t.Fatalf("first tick does not refuse the unknown lane: %q", err1.String())
	}
	var out2, err2 bytes.Buffer
	in.Stdout, in.Stderr = &out2, &err2
	if code := Fill(in); code != 0 {
		t.Fatalf("second tick exit = %d, stderr=%q", code, err2.String())
	}
	if strings.Contains(err2.String(), "FILL REFUSED") {
		t.Fatalf("the second tick refused the same card again over an unchanged lanes file: %q", err2.String())
	}
	if got := markersIn(t, ready, "card-001.md.refused-*"); len(got) != 1 {
		t.Fatalf("refusal markers = %v, want exactly one", got)
	}

	// The lanes file is edited: a new mtime is a new answer, so the refusal speaks again
	// and the stale marker is gone.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(lanes, future, future); err != nil {
		t.Fatal(err)
	}
	var out3, err3 bytes.Buffer
	in.Stdout, in.Stderr = &out3, &err3
	if code := Fill(in); code != 0 {
		t.Fatalf("third tick exit = %d, stderr=%q", code, err3.String())
	}
	if !strings.Contains(err3.String(), "FILL REFUSED card=card-001.md lane=ghost") {
		t.Fatalf("an edited lanes file did not make the refusal speak again: %q", err3.String())
	}
	if got := markersIn(t, ready, "card-001.md.refused-*"); len(got) != 1 {
		t.Fatalf("refusal markers = %v, want exactly one (the stale one is removed)", got)
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
