package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSource answers one scripted view per sweep: the nth call for a PR gets the nth view,
// the last standing for every call after it. No network, no gh.
type fakeSource struct {
	views map[int][]PRView
	calls map[int]int
	repos []string
}

func (f *fakeSource) View(repo string, pr int) (PRView, error) {
	f.repos = append(f.repos, repo)
	seq := f.views[pr]
	if len(seq) == 0 {
		return PRView{}, fmt.Errorf("no such pull request %d", pr)
	}
	i := f.calls[pr]
	f.calls[pr]++
	if i >= len(seq) {
		i = len(seq) - 1
	}
	v := seq[i]
	v.Number = pr
	return v, nil
}

// fakeEnqueuer records every enqueue; the test asserts it was called exactly once.
type fakeEnqueuer struct{ calls []int }

func (f *fakeEnqueuer) Enqueue(repo string, pr int) error {
	f.calls = append(f.calls, pr)
	return nil
}

func green() []PRCheck {
	return []PRCheck{{Name: "fast", State: "SUCCESS"}, {Name: "vet", State: "SUCCESS"}}
}
func queued() []PRCheck {
	return []PRCheck{{Name: "fast", State: "QUEUED"}, {Name: "vet", State: "SUCCESS"}}
}

func sweepQueue(t *testing.T, rows ...LedgerRow) string {
	t.Helper()
	queue := t.TempDir()
	for _, r := range rows {
		if err := AppendLedger(queue, r); err != nil {
			t.Fatal(err)
		}
	}
	return queue
}

func runSweep(t *testing.T, queue string, src PRSource, enq Enqueuer, at time.Time) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Sweep(SweepInput{
		Repo: "mas-bandwidth/nova-tools", Queue: queue, Source: src, Enqueuer: enq,
		Now: func() time.Time { return at }, Stdout: &out, Stderr: &errs,
	})
	return code, out.String(), errs.String()
}

// approval-is-a-ledger-row-not-a-moment: an APPROVE row whose checks are QUEUED at sweep 1
// is enqueued at sweep 2 when they are green, and never enqueued twice — pit stop 3 bug 4,
// 51 approved PRs logged "(not merged)" and never revisited (issue #828, class D).
func TestSweepEnqueuesWhenChecksTurnGreenAndNeverTwice(t *testing.T) {
	queue := sweepQueue(t, LedgerRow{PR: 7, Head: "h1", Card: "card-9.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"})
	src := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{7: {
		{State: "OPEN", Head: "h1", Title: "fix #601", Checks: queued()},
		{State: "OPEN", Head: "h1", Title: "fix #601", Checks: green()},
		{State: "OPEN", Head: "h1", Title: "fix #601", Checks: green(), AutoMerge: true},
	}}}
	enq := &fakeEnqueuer{}
	at := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)

	code, out, errs := runSweep(t, queue, src, enq, at)
	if code != 0 {
		t.Fatalf("sweep 1 exit = %d, stderr=%s", code, errs)
	}
	if len(enq.calls) != 0 {
		t.Errorf("sweep 1 enqueued %v; a QUEUED check is not green", enq.calls)
	}
	if !strings.Contains(out, "pending=1") {
		t.Errorf("sweep 1 line does not count the pending row: %q", out)
	}

	if _, out, errs = runSweep(t, queue, src, enq, at.Add(time.Minute)); !strings.Contains(out, "enqueued=1") {
		t.Errorf("sweep 2 did not enqueue the now-green row: out=%q stderr=%q", out, errs)
	}
	if len(enq.calls) != 1 || enq.calls[0] != 7 {
		t.Fatalf("enqueue calls after sweep 2 = %v, want [7]", enq.calls)
	}

	if _, out, _ = runSweep(t, queue, src, enq, at.Add(2*time.Minute)); len(enq.calls) != 1 {
		t.Errorf("sweep 3 enqueued again: %v (a row marked enqueued is never enqueued twice)", enq.calls)
	}
	if !strings.Contains(out, "enqueued=0") {
		t.Errorf("sweep 3 line = %q, want enqueued=0", out)
	}
	rows, err := ReadLedger(queue)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("the ledger is append-only and records the mark: rows=%d", len(rows))
	}
	last := rows[len(rows)-1]
	if last.EnqueuedAt == "-" || last.EnqueuedAt == "" {
		t.Errorf("the last ledger row carries no enqueued_at: %+v", last)
	}
}

// a head that moved owes a read, and a merged or closed PR closes its row.
func TestSweepMarksStaleHeadAndClosesMergedRows(t *testing.T) {
	queue := sweepQueue(t,
		LedgerRow{PR: 7, Head: "h1", Card: "card-9.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 8, Head: "h2", Card: "card-10.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
	)
	src := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{
		7: {{State: "OPEN", Head: "h1-moved", Checks: green()}},
		8: {{State: "MERGED", Head: "h2", Checks: green()}},
	}}
	enq := &fakeEnqueuer{}
	code, out, errs := runSweep(t, queue, src, enq, time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if len(enq.calls) != 0 {
		t.Errorf("enqueued %v; neither a moved head nor a merged PR is enqueued", enq.calls)
	}
	if !strings.Contains(out, "stale=1") || !strings.Contains(out, "closed=1") {
		t.Errorf("sweep line = %q, want stale=1 and closed=1", out)
	}
	if open := openRowsOf(t, queue); len(open) != 0 {
		t.Errorf("open rows after the sweep = %d, want 0 (both rows are disposed)", len(open))
	}
}

// the holds are lines the code reads, never prose in POLICY.md: a draft, a `hold` label and
// a red-test PR are each held, and nothing is enqueued — pit stop 3 bug 5 (class B).
func TestSweepHoldsDraftsLabelsAndRedTestPRs(t *testing.T) {
	queue := sweepQueue(t,
		LedgerRow{PR: 1, Head: "a", Card: "card-1.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 2, Head: "b", Card: "card-2.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 3, Head: "c", Card: "card-3.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 4, Head: "d", Card: "card-4.md", Verdict: "HOLD", At: "2026-09-16T17:00:00Z"},
	)
	src := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{
		1: {{State: "OPEN", Head: "a", IsDraft: true, Checks: green()}},
		2: {{State: "OPEN", Head: "b", Labels: []string{"hold"}, Checks: green()}},
		3: {{State: "OPEN", Head: "c", Title: "nova-work red tests for the lease", Checks: green()}},
		4: {{State: "OPEN", Head: "d", Checks: green()}},
	}}
	enq := &fakeEnqueuer{}
	_, out, _ := runSweep(t, queue, src, enq, time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC))
	if len(enq.calls) != 0 {
		t.Fatalf("enqueued %v; a draft, a hold label, a red-test title and a HOLD verdict are all held", enq.calls)
	}
	if !strings.Contains(out, "held=4") {
		t.Errorf("sweep line = %q, want held=4", out)
	}
}

// the sweep seeds its ledger from the APPROVED file the manager tier writes, so an approval
// recorded before the ledger existed is still walked every tick.
func TestSweepSeedsTheLedgerFromApproved(t *testing.T) {
	queue := t.TempDir()
	body := "mas-bandwidth/nova-tools 11 h11\nmas-bandwidth/nova-work 12 h12\n"
	if err := os.WriteFile(filepath.Join(queue, "APPROVED"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{
		11: {{State: "OPEN", Head: "h11", Checks: green()}},
	}}
	enq := &fakeEnqueuer{}
	_, out, errs := runSweep(t, queue, src, enq, time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC))
	if !strings.Contains(out, "seeded=1") {
		t.Errorf("sweep line = %q, want seeded=1 (the other repo's row is not this repo's)", out)
	}
	if len(enq.calls) != 1 || enq.calls[0] != 11 {
		t.Errorf("enqueue calls = %v, want [11]; stderr=%s", enq.calls, errs)
	}
}

// bounded output: one SWEEP line on stdout whatever the ledger's size.
func TestSweepPrintsOneLine(t *testing.T) {
	var rows []LedgerRow
	views := map[int][]PRView{}
	for i := 1; i <= 60; i++ {
		rows = append(rows, LedgerRow{PR: i, Head: "h", Card: "card.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"})
		views[i] = []PRView{{State: "OPEN", Head: "h", Checks: green()}}
	}
	queue := sweepQueue(t, rows...)
	var out, errs bytes.Buffer
	code := Sweep(SweepInput{
		Repo: "mas-bandwidth/nova-tools", Queue: queue,
		Source: &fakeSource{calls: map[int]int{}, views: views}, Enqueuer: &fakeEnqueuer{},
		Now:    func() time.Time { return time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC) },
		Stdout: &out, Stderr: &errs,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs.String())
	}
	if n := len(strings.Split(strings.TrimRight(out.String(), "\n"), "\n")); n != 1 {
		t.Errorf("stdout is %d lines at 60 open rows, want 1:\n%s", n, out.String())
	}
	if errs.Len() != 0 {
		t.Errorf("stderr is not empty: %q", errs.String())
	}
}

// a missing queue directory is a refusal with a remedy, never a silent zero.
func TestSweepRefusesAMissingQueue(t *testing.T) {
	var out, errs bytes.Buffer
	code := Sweep(SweepInput{
		Repo: "mas-bandwidth/nova-tools", Queue: filepath.Join(t.TempDir(), "nope"),
		Source: &fakeSource{calls: map[int]int{}}, Enqueuer: &fakeEnqueuer{},
		Now: time.Now, Stdout: &out, Stderr: &errs,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs.String(), "SWEEP REFUSED") || !strings.Contains(errs.String(), "(") {
		t.Errorf("the refusal names no remedy: %q", errs.String())
	}
}

func openRowsOf(t *testing.T, queue string) []LedgerRow {
	t.Helper()
	rows, err := ReadLedger(queue)
	if err != nil {
		t.Fatal(err)
	}
	return OpenRows(rows)
}
