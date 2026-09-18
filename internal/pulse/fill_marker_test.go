package pulse

// The launched-card marker (the lane half of edge 10, 2026-09-18): the card under
// --launched carries a marker beside it naming the lane it holds, the bench it went to and
// the session that cut it. The lane a live card holds is read from that marker, not by
// parsing the card again: release is by lane name, and a card whose text a worker rewrote
// -- or a queue whose cards a hand edited -- still releases the lane it actually took.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFillWritesTheLaunchedMarkerCarryingTheLaneAndSession(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Benches: []string{"bench-a"}, Once: true, Session: "s-42",
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: &laneLauncher{},
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(launched, "card-001.md.launched"))
	if err != nil {
		t.Fatalf("no launched marker beside the card: %v", err)
	}
	for _, want := range []string{"lane=pulse", "bench=bench-a", "label=card-001", "session=s-42"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the launched marker does not carry %q:\n%s", want, raw)
		}
	}
}

// The marker is what holds the lane. A live card whose own text no longer names the lane
// still holds it, so the release is by lane name and never by re-reading the card.
func TestFillReadsTheLiveLaneFromTheMarkerNotTheCard(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}
	// A live card under --launched whose text names no lane, and a marker that does.
	if err := os.WriteFile(filepath.Join(launched, "card-001.md"), []byte("RESULT: CARD-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launched, "card-001.md.launched"), []byte("lane=pulse\nbench=bench-a\nlabel=card-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCard(t, ready, "card-002.md", "RESULT: CARD-2\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Benches: []string{"bench-a"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0: the lane is held by the marker, not by the card's text", len(l.calls))
	}
	if !strings.Contains(out.String(), "FILL HELD card=card-002.md lane=pulse live=card-001.md") {
		t.Fatalf("the ready card was not held behind the marker's lane: %q", out.String())
	}
}

// A launcher that fails releases the lane, and the marker goes with the card: a marker left
// beside a card that went back to --ready holds a lane nobody is running.
func TestFillRemovesTheLaunchedMarkerWhenTheLauncherFails(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	var out, errb bytes.Buffer
	Fill(FillInput{
		Ready: ready, Launched: launched, Lanes: lanes,
		Benches: []string{"bench-a"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: &failingLauncher{err: fmt.Errorf("exit status 7")},
	})
	if _, err := os.Stat(filepath.Join(launched, "card-001.md.launched")); !os.IsNotExist(err) {
		t.Error("the launched marker outlived the card it was beside")
	}
	if _, err := os.Stat(filepath.Join(ready, "card-001.md")); err != nil {
		t.Errorf("the card did not go back to --ready: %v", err)
	}
}
