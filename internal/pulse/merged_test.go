package pulse

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSweepWritesTheMergedRecordStatusReads is the seam `status --oneline` names: its
// merges=<n> counts the day's lines in <queue>/MERGED (statusline.go's countDay), and until
// this card nothing wrote that file, so every day read merges=0 however much had landed.
//
// The mutation that matters: writing the line for every closed row makes a PR somebody
// closed unmerged count as a merge, and the day's one number about landed work is wrong.
func TestSweepWritesTheMergedRecordStatusReads(t *testing.T) {
	queue := sweepQueue(t,
		LedgerRow{PR: 812, Head: "h1", Card: "card-9.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 813, Head: "h2", Card: "card-10.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
	)
	src := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{
		812: {{State: "MERGED", Head: "h1", Title: "fix #601"}},
		813: {{State: "CLOSED", Head: "h2", Title: "fix #602"}},
	}}
	at := time.Date(2026, 9, 16, 18, 30, 0, 0, time.UTC)

	code, out, errs := runSweep(t, queue, src, &fakeEnqueuer{}, at)
	if code != 0 {
		t.Fatalf("sweep exit = %d: %s%s", code, out, errs)
	}
	if !strings.Contains(out, "closed=2") {
		t.Errorf("SWEEP line = %q, want closed=2", out)
	}

	lines := readLines(filepath.Join(queue, "MERGED"))
	if len(lines) != 1 {
		t.Fatalf("MERGED holds %d lines, want 1 (the merged PR, never the closed one): %v", len(lines), lines)
	}
	want := at.UTC().Format(time.RFC3339) + "\tMERGED\tmas-bandwidth/nova-tools#812"
	if lines[0] != want {
		t.Errorf("MERGED line = %q, want %q", lines[0], want)
	}
	// And the line is what status --oneline counts: stamped with the day, first field.
	if n := countDay(queue, "MERGED", "2026-09-16", ""); n != 1 {
		t.Errorf("status counts merges=%d for the day, want 1", n)
	}
	// A second sweep does not count the same merge twice: the row is closed and is not walked again.
	if _, _, _ = runSweep(t, queue, src, &fakeEnqueuer{}, at.Add(time.Minute)); len(readLines(filepath.Join(queue, "MERGED"))) != 1 {
		t.Errorf("a closed row was swept again and merges double-counted: %v", readLines(filepath.Join(queue, "MERGED")))
	}
}
