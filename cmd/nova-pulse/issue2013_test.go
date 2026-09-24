package main

// fill verb at the command's own edge: nova-tools #2013 (rows of `.failed-<n>` markers
// making every counter read the queue as full while it idled -- tools19 found 49 stale
// ones, one launcher bug produced 1,275 in ten minutes). The fix lives in two places: an
// EXTERNAL markers directory beside --ready that the tick writes to, and `ready=` on the
// FILL line that counts only card-*.md. This test exercises the verb's own boundary, the
// one the load-test dealer tripped over, by running `nova-pulse fill` end to end.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dirNamesIn returns every entry in a directory, in sorted order. It is the test's answer
// to "what is the depth of this directory?": for the load-test dealer the answer was
// `ls | wc -l`, and that counted markers as cards.
func dirNamesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestIssue2013: fill's --ready directory holds CARDS, never markers, and the FILL line's
// ready= counts cards not files. A failing launcher puts the card back to --ready with a
// `.failed-1` marker in <ready>-markers (NOT inside --ready), the marker carries the
// attempt and the reason, and ready= on the FILL line is 1 -- the same number a `--ready`
// of one card and zero markers reports, because the queue's depth is its cards.
func TestIssue2013(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	launched := filepath.Join(dir, "launched")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMainFile(t, ready, "card-001.md", "a card\n")
	// A launcher that always fails with an exit and a reason on its last stderr line.
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stderr: "no such bench", Exit: 7}})

	var out, errb bytes.Buffer
	code := run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once",
		"--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	// The launcher fails every launch on the only bench: --once returns 1 because every
	// bench failed the tick, which is the documented signal for a red fleet, not a bug.
	if code != 0 && code != 1 {
		t.Fatalf("fill --once exit = %d, want 0 or 1; stdout=%q stderr=%q", code, out.String(), errb.String())
	}

	// 1. --ready holds CARDS, never markers. The card is back; nothing else is.
	if got := dirNamesIn(t, ready); len(got) != 1 || got[0] != "card-001.md" {
		t.Fatalf("--ready holds %v, want exactly [card-001.md]: the load-test dealer that read 1,275 markers as 1,275 cards made every counter say the benches were full while they idled", got)
	}

	// 2. The marker is in the directory BESIDE --ready, named for it.
	markers := filepath.Join(dir, "ready-markers")
	want := []string{"card-001.md.failed-1"}
	if got := dirNamesIn(t, markers); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("the markers directory %s holds %v, want exactly %v: failure markers live beside the queue, never inside --ready (#2013)", markers, got, want)
	}
	raw, err := os.ReadFile(filepath.Join(markers, want[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, mustHave := range []string{"attempt=1", "no such bench"} {
		if !strings.Contains(string(raw), mustHave) {
			t.Errorf("the marker %s does not carry %q:\n%s", want[0], mustHave, raw)
		}
	}

	// 3. The FILL line reports queue depth as CARDS. One card, no markers: ready=1.
	if !strings.Contains(out.String(), "ready=1") {
		t.Fatalf("the FILL line does not report a queue depth of 1 card; the load-test incident was a depth of 1,275 that meant zero:\n%s", out.String())
	}
	// ...and not 2. Two is what a `ls | wc -l` would have said, and it is exactly
	// the number that blinded the dealer.
	if strings.Contains(out.String(), "ready=2") {
		t.Fatalf("the FILL line reports depth as files (ready=2):\n%s", out.String())
	}
	// 4. The card counts under failed=, not launched= -- a launcher that failed
	// launched nothing and the dealer has to see why.
	if !strings.Contains(out.String(), "launched=0") || !strings.Contains(out.String(), "failed=1") {
		t.Fatalf("the FILL line does not name a failed launch:\n%s", out.String())
	}
}

// TestIssue2013ReapsTheMarkerWhoseCardNoLongerSitsInReady: tools19's 49 stale markers were
// the same shape -- a card launched once, never again, and its failures sitting in the
// queue for a counter to trip over. Fill's tick takes markers whose card has left --ready
// at the top of the tick, before any card is dealt.
func TestIssue2013ReapsTheMarkerWhoseCardNoLongerSitsInReady(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ready-markers"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Stale markers: their cards are NOT in --ready. A launch that succeeds would
	// happen before fill runs, but here they have been withdrawn -- harvested,
	// taken out by hand, gone.
	for _, stale := range []string{
		"card-900.md.failed-1",
		"card-900.md.failed-2",
		"card-901.md.refused-7",
	} {
		if err := os.WriteFile(filepath.Join(dir, "ready-markers", stale), []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// One card the tick will actually deal: it has nothing to fail and exits 0.
	// Mark its slot store so the bench reads 1.
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Exit: 0}})

	var out, errb bytes.Buffer
	code := run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once",
		"--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill --once exit = %d, want 0; stdout=%q stderr=%q", code, out.String(), errb.String())
	}

	got := dirNamesIn(t, filepath.Join(dir, "ready-markers"))
	for _, gone := range []string{"card-900.md.failed-1", "card-900.md.failed-2", "card-901.md.refused-7"} {
		for _, name := range got {
			if name == gone {
				t.Errorf("the stale marker %s outlived the card it belonged to: markers=%v", gone, got)
			}
		}
	}
	if !strings.Contains(out.String(), fmt.Sprintf("FILL REAPED")) {
		t.Errorf("the tick reaped stale markers and did not say so:\n%s", out.String())
	}
}
