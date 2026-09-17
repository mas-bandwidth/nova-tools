package pulse

// The status verb's convergence replays (SPEC-PULSE.md, "Status", Glenn 2026-09-15):
// the contraction ratio per stream every tick, and EXPANDING when a stream's ratio has
// been above 1 for two consecutive hours. TICKS is one line per tick per stream, written
// by `run`; CONVERGENCE.tsv holds the rolling two-hour window.
//
// Replays: status-stream-prints-contraction-ratio, status-expanding-after-two-hours-above-one,
// status-one-hundred-minutes-is-not-expanding.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTicks writes one TICKS line per minute for n minutes ending at now, one record per
// stream with the given opened/closed counts.
func writeTicks(t *testing.T, queue string, now time.Time, minutes int, streams map[string][2]int) {
	t.Helper()
	var b strings.Builder
	for i := minutes; i >= 0; i-- {
		at := now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)
		for _, name := range sortedKeys(streams) {
			counts := streams[name]
			fmt.Fprintf(&b, "at=%s\tstream=%s\topened=%d\tclosed=%d\n", at, name, counts[0], counts[1])
		}
	}
	if err := os.WriteFile(filepath.Join(queue, "TICKS"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// status-stream-prints-contraction-ratio: a contracting stream prints its ratio, counted
// over the tick window, and is not EXPANDING.
func TestStatusStreamPrintsContractionRatio(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	queue, roots, specs, now := setupStatus(t, rootA)
	fakeGh(t, specs, "[]")
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeSlots(t, rootA, []string{"free"})
	writeTicks(t, queue, now, 30, map[string][2]int{"alpha": {1, 3}})

	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "STATUS STREAM alpha opened=31 closed=93 ratio=0.33") {
		t.Errorf("a contracting stream wants its ratio line, got:\n%s", out)
	}
	if strings.Contains(out, "STATUS EXPANDING") {
		t.Errorf("a contracting stream must not be EXPANDING:\n%s", out)
	}
}

// status-expanding-after-two-hours-above-one: a stream above 1 for every tick in two hours
// prints EXPANDING and the verb exits 2.
func TestStatusExpandingAfterTwoHoursAboveOne(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	queue, roots, specs, now := setupStatus(t, rootA)
	fakeGh(t, specs, "[]")
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeSlots(t, rootA, []string{"free"})
	writeTicks(t, queue, now, 120, map[string][2]int{"alpha": {3, 1}})

	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (EXPANDING)", code)
	}
	if !strings.Contains(out, "STATUS STREAM alpha opened=363 closed=121 ratio=3.00") {
		t.Errorf("EXPANDING stream wants its ratio line, got:\n%s", out)
	}
	if !strings.Contains(out, "STATUS EXPANDING stream=alpha hours=2") {
		t.Errorf("two hours above 1 wants EXPANDING, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(queue, "CONVERGENCE.tsv")); err != nil {
		t.Errorf("the rolling two-hour window wants CONVERGENCE.tsv: %v", err)
	}
}

// status-one-hundred-minutes-is-not-expanding: above 1 for 100 minutes is not yet two hours.
func TestStatusOneHundredMinutesIsNotExpanding(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	queue, roots, specs, now := setupStatus(t, rootA)
	fakeGh(t, specs, "[]")
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeSlots(t, rootA, []string{"free"})
	writeTicks(t, queue, now, 100, map[string][2]int{"alpha": {3, 1}})

	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (not yet two hours)", code)
	}
	if strings.Contains(out, "STATUS EXPANDING") {
		t.Errorf("100 minutes above 1 must not be EXPANDING:\n%s", out)
	}
}
