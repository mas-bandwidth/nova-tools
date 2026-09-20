package pulse

// fillreap_test.go is the red test for #2013: a failure marker is not a card, it does not
// live in the queue, and it does not outlive the failure it records.
//
// tools19 found 49 stale `.failed-n` markers making every counter say the fleet was fed
// while it idled; on the night of the 2026-09-20 load test one launcher bug produced 1,275
// of them in ten minutes and they blinded the hand-built dealer, which read a directory
// listing as queue depth and believed the Linux benches were full.
//
// The rule now: markers live in their OWN directory beside the queue -- never in --ready --
// they carry the attempt and the reason as before, and they are reaped when the card they
// belong to is relaunched or withdrawn. A ready directory holds cards, so anything that
// counts it counts cards.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reapFill runs one tick over one ready directory with the given launcher and returns the
// markers directory, stdout and stderr.
func reapFill(t *testing.T, dir, ready, launched string, l CardLauncher) (string, string, string) {
	t.Helper()
	markers := filepath.Join(dir, "ready-markers")
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: l,
	})
	if code != 0 && code != 1 {
		t.Fatalf("fill exit = %d, want 0 or 1; stderr=%q", code, errb.String())
	}
	return markers, out.String(), errb.String()
}

func namesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sortStrings(out)
	return out
}

// TestFillKeepsFailureMarkersOutOfTheReadyDirectory: a launcher that fails puts the card
// back in --ready with NOTHING beside it; the marker, with the attempt and the reason, is in
// the markers directory.
func TestFillKeepsFailureMarkersOutOfTheReadyDirectory(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	markers, _, _ := reapFill(t, dir, ready, launched, &failingLauncher{err: fmt.Errorf("exit status 125")})

	if got := namesIn(t, ready); len(got) != 1 || got[0] != "card-001.md" {
		t.Fatalf("the ready directory holds %v, want exactly [card-001.md]: a marker is not a card and does not live in the queue", got)
	}
	if got := namesIn(t, markers); len(got) != 1 || got[0] != "card-001.md.failed-1" {
		t.Fatalf("the markers directory %s holds %v, want exactly [card-001.md.failed-1]", markers, got)
	}
	raw, err := os.ReadFile(filepath.Join(markers, "card-001.md.failed-1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"attempt=1", "exit status 125"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the marker does not carry %q:\n%s", want, raw)
		}
	}
}

// TestFillReapsAFailureMarkerWhenTheCardRelaunches: the marker records a failure, not a
// history. A tick that fails leaves one; the next tick, whose launcher works, launches the
// card and takes the marker with it.
func TestFillReapsAFailureMarkerWhenTheCardRelaunches(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")

	markers, _, _ := reapFill(t, dir, ready, launched, &failingLauncher{err: fmt.Errorf("exit status 125")})
	if got := namesIn(t, markers); len(got) != 1 {
		t.Fatalf("after the failed tick the markers directory holds %v, want one marker", got)
	}
	if _, _, _ = reapFill(t, dir, ready, launched, &laneLauncher{}); true {
		if got := namesIn(t, markers); len(got) != 0 {
			t.Fatalf("after the card relaunched the markers directory still holds %v, want none", got)
		}
	}
}

// TestFillReapsMarkersForCardsThatAreNoLongerReady: the 1,275. A marker whose card has left
// --ready -- harvested, withdrawn, moved by a hand -- is reaped at the top of the tick, and
// the tick says how many it took.
func TestFillReapsMarkersForCardsThatAreNoLongerReady(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	markers := filepath.Join(dir, "ready-markers")
	if err := os.MkdirAll(markers, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"card-900.md.failed-1", "card-900.md.failed-2", "card-901.md.refused-7"} {
		if err := os.WriteFile(filepath.Join(markers, name), []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// One card that IS ready, with a marker of its own: it stays, because it is still waiting.
	writeCard(t, ready, "card-001.md", "a card\n")
	if err := os.WriteFile(filepath.Join(markers, "card-001.md.failed-1"), []byte("live\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, out, _ := reapFill(t, dir, ready, launched, &failingLauncher{err: fmt.Errorf("exit status 125")})

	got := namesIn(t, markers)
	for _, gone := range []string{"card-900.md.failed-1", "card-900.md.failed-2", "card-901.md.refused-7"} {
		for _, name := range got {
			if name == gone {
				t.Errorf("the stale marker %s outlived the card it belonged to; markers=%v", gone, got)
			}
		}
	}
	if !strings.Contains(out, "FILL REAPED") {
		t.Errorf("the tick reaped stale markers and did not say so:\n%s", out)
	}
}

// TestFillReapsLegacyMarkersLeftInsideTheReadyDirectory: every marker written before this
// rule is sitting in --ready on nine benches right now. The tick takes them out, so the
// directory a counter reads is cards and nothing else.
func TestFillReapsLegacyMarkersLeftInsideTheReadyDirectory(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	for _, name := range []string{"card-001.md.failed-1", "card-001.md.failed-2", "card-900.md.failed-1", "card-900.md.refused-3"} {
		if err := os.WriteFile(filepath.Join(ready, name), []byte("legacy\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _ = reapFill(t, dir, ready, launched, &laneLauncher{})
	if got := namesIn(t, ready); len(got) != 0 {
		t.Fatalf("the ready directory still holds %v after the card launched, want nothing: "+
			"markers written before the rule are reaped where they lie", got)
	}
}

// TestFillCountsCardsNotFilesAsQueueDepth: the depth on the FILL line is card-*.md and
// nothing else, with a directory of markers beside it.
func TestFillCountsCardsNotFilesAsQueueDepth(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 3; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	_, out, _ := reapFill(t, dir, ready, launched, &failingLauncher{err: fmt.Errorf("exit status 125")})
	// All three came back, so the depth is three cards -- not three cards and three markers.
	if !strings.Contains(out, "ready=3") {
		t.Fatalf("the FILL line does not report a depth of 3 cards:\n%s", out)
	}
}
