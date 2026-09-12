package swarm

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A REPORT THAT COULD NOT BE READ IS NOT A REPORT THAT WAS NEVER PUBLISHED.
//
// The same class this branch closed for exit.json, one file over. RESULT.md is published by
// the harness the way every durable record here is written -- a .tmp beside it, fsynced,
// then renamed over the path -- and on Windows a read landing inside that replace window
// fails ERROR_ACCESS_DENIED for microseconds. `finish` and `Pool.copyReport` both read it
// with a bare os.ReadFile and both read that microsecond as a FACT: `result=no-result`,
// `dest=failed`, the findings the worker had already published thrown away, and a
// MarkerNoResult written into reports/ that outlives the run.
//
// Asserted here with no Windows and no race. The collision is produced through the
// forceTransientIO seam over a RESULT.md that is a DIRECTORY -- this package's portable
// stand-in for a pending replace -- and it lasts a few polls, far LESS than SteadyWindow.
// The rule is that the job is still classified from the report's CONTENT.
func TestAReportWhoseReadsCollideIsStillClassifiedFromItsContent(t *testing.T) {
	const body = "# a published report\n\n## Head\nfindings: 1\nrepo: o/n\nrev: abc\nit published before its dispatcher read it.\n\n" +
		"## Findings\n- one thing, x.go:1\n\n## Per item\n| item | state | evidence |\n| --- | --- | --- |\n| an item | red | x.go:1 |\n"

	t.Run("finish classifies the job from the report", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "pool"), 0o755); err != nil {
			t.Fatal(err)
		}
		p, err := OpenPool(filepath.Join(dir, "pool"))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		sc := Sidecar{ID: "20260912T000000Z-task-abc123", Files: 1, Unmetered: true, Deadline: "30s", Started: Stamp(now), Slot: 1, Job: filepath.Join(dir, "job")}
		if err := p.Add([]byte("a task\n"), sc); err != nil {
			t.Fatal(err)
		}
		if err := p.Claim(sc.ID, Pending, Running); err != nil {
			t.Fatal(err)
		}
		jobDir := sc.Job
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Everything else this pass reads is THERE, so the only unread record is the one
		// under test: the supervisor's completion evidence, the harness log, the note file.
		if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: 0, End: EndDone, Nonce: "abc123"}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte("the harness said nothing of note\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(NotePath(jobDir), nil, 0o644); err != nil {
			t.Fatal(err)
		}

		if err := p.Reserve(1, sc.ID, jobDir, "abc123", os.Getpid(), now); err != nil {
			t.Fatal(err)
		}

		hits := collideUntilRead(t, ResultPath(jobDir), body)
		in := RunInput{Pool: p, Stdout: io.Discard, Stderr: io.Discard, Now: func() time.Time { return now }}
		r := &running{sc: sc, slot: 1, nonce: "abc123", jobDir: jobDir, started: now, deadline: 30 * time.Second}
		line, end, dest := in.finish(r, map[int]bool{}, now)

		if strings.Contains(line, "result="+ClassNoResult) {
			t.Errorf("a RESULT.md that could not be read for an instant was reported as one that was never published:\n%s", line)
		}
		if !strings.Contains(line, "findings=1") {
			t.Errorf("the report's own findings are what the line carries; it said:\n%s", line)
		}
		if end != EndDone || dest != Done {
			t.Errorf("a job with rc=0 and a published report ends done/done, and this one ended %s/%s:\n%s", end, dest, line)
		}
		// The fixture's own guard, LAST: a reader that never asked whether its failure was
		// a collision never waited one out, which is the bug itself and is reported above.
		if hits.Load() == 0 {
			t.Error("no read of this RESULT.md ever went through the collision wait")
		}
	})

	t.Run("finalize retains the report rather than a marker", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "pool"), 0o755); err != nil {
			t.Fatal(err)
		}
		p, err := OpenPool(filepath.Join(dir, "pool"))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		sc := Sidecar{ID: "20260912T000001Z-task-def456", Files: 1, Unmetered: true, Deadline: "30s", Started: Stamp(now)}
		jobDir := filepath.Join(dir, "job")
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		hits := collideUntilRead(t, ResultPath(jobDir), body)
		fin, err := p.Finalize(Ending{Sidecar: sc, JobDir: jobDir, Provider: "fake", Model: "fake-model",
			End: EndDone, RC: 0, Started: now, Ended: now, Usage: ProviderUsage{Values: map[string]string{}}})
		if err != nil {
			t.Fatal(err)
		}
		if !fin.Published || fin.Class == ClassNoResult {
			t.Errorf("rule 12's retained copy was decided by a collision: published=%t class=%s", fin.Published, fin.Class)
		}
		raw, kind, err := p.RetainedReport(sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if kind != CopiedResult || string(raw) != body {
			t.Errorf("the retained record is %s, and the run kept no copy of a report that was on disk", kind)
		}
		// The fixture's own guard, LAST: a reader that never asked whether its failure was
		// a collision never waited one out, which is the bug itself and is reported above.
		if hits.Load() == 0 {
			t.Error("no read of this RESULT.md ever went through the collision wait")
		}
	})
}

// collideUntilRead makes the first few reads of `path` fail and then ENDS the collision, so
// a test can assert the rule with no Windows and no race. `path` becomes a DIRECTORY -- this
// package's portable stand-in for the microseconds a Windows replace is pending -- and the
// reader meets the two things a real collision has: a read that fails, and a path that holds
// still again a few polls later, far inside SteadyWindow.
//
// THE SEAM IS ALSO WHAT LIFTS THE COLLISION, and that is the whole of its correctness. A
// fixture that replaced the path from a goroutine and then flipped a flag judges the read
// ALREADY IN FLIGHT by the platform's rule -- that read failed while the stand-in was still
// there and asks this seam afterwards -- and hands the caller a stale error it was never
// given the chance to re-read past. Here the replace happens INSIDE the call that answers
// "yes, a collision", so the reader that paid for it is by construction the one whose retry
// finds the record. No goroutine, no clock, nothing to lose a race to.
//
// What a directory read fails WITH is the platform's business (EISDIR here, something else
// on Windows) and the rule under test is not, so while the stand-in is in place EVERY failed
// read is a collision -- the shape `TestTheLaunchHandshakeEndsAtItsOwnTimeoutWhenEveryReadCollides`
// already uses on that runner. Two things keep that from arming on somebody else's read: a
// record that is GONE answers ErrNotExist and is an ANSWER, never a collision (the package's
// own `missing` rule, and true on every platform), and each fixture leaves every OTHER record
// of its job present and readable. Once the record is back the platform's rule decides again,
// so nothing here loops twice.
func collideUntilRead(t *testing.T, path string, body string) *atomic.Int64 {
	t.Helper()
	// A path that is already a record is REPLACED by the stand-in, so a collision can be
	// armed over a file a reader has read once already -- which is what a rehash meets.
	_ = os.Remove(path)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	var restored atomic.Bool
	forceTransientIO = func(err error) bool {
		if err == nil || restored.Load() || errors.Is(err, fs.ErrNotExist) {
			return transientIO(err)
		}
		// A few polls of collision -- enough that a reader which does not wait one out is
		// caught, and orders of magnitude less than SteadyWindow -- and then the record is
		// put back. The replace is retried on the next turn if it does not land, because a
		// directory with a reader in it does not come away on the first ask on Windows.
		if hits.Add(1) >= 3 {
			_ = os.RemoveAll(path)
			if err := os.WriteFile(path, []byte(body), 0o644); err == nil {
				restored.Store(true)
			}
		}
		return true
	}
	t.Cleanup(func() { forceTransientIO = nil })
	return &hits
}

// A COLLISION IS NOT A REVISION, AND AN UNREADABLE REPORT IS NOT AN UNPUBLISHED ONE.
//
// The same class one tool over. `triage` reads a report THREE times through paths this
// package publishes by rename -- the retained copy, the live RESULT.md of a running job,
// and the rehash that proves the buffer it parsed is still what is on disk -- and all three
// were a bare `os.ReadFile`. On Windows that makes a replace collision indistinguishable
// from the one thing rule 16 exists to catch, a writer appending IN PLACE:
//
//   - a retained or live copy that collided was counted `no_result`, and the job's findings
//     never reached the page a coordinator reads, and
//   - a rehash that collided printed `TRIAGE SKIPPED id=… changed while read` over a report
//     that had not changed by one byte.
func TestATriageWhoseReportReadsCollideStillFoldsTheReport(t *testing.T) {
	t.Run("the retained copy collides", func(t *testing.T) {
		dir := t.TempDir()
		p, id := revisionPool(t, dir)
		hits := collideUntilRead(t, filepath.Join(p.ReportsDir(id), CopiedResult), report())

		out := mustTriage(t, TriageInput{Pool: p})
		if !strings.Contains(out, "TRIAGE REPORT id="+id) {
			t.Errorf("a retained report that could not be read for an instant was not folded:\n%s", out)
		}
		if strings.Contains(out, "no_result=1") {
			t.Errorf("a collision was counted as a job that published nothing:\n%s", out)
		}
		if hits.Load() == 0 {
			t.Error("no read of this retained copy ever went through the collision wait")
		}
	})

	t.Run("the live report of a running job collides", func(t *testing.T) {
		dir := t.TempDir()
		p := emptyPool(t, dir)
		jobDir := filepath.Join(dir, "job")
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sc := Sidecar{ID: NewID(time.Now().UTC(), "live"), Files: 1, Tokens: 1000, Job: jobDir}
		if err := p.Add([]byte("a task"), sc); err != nil {
			t.Fatal(err)
		}
		if err := p.Claim(sc.ID, Pending, Running); err != nil {
			t.Fatal(err)
		}
		// A RUNNING job has no retained copy: that read answers ErrNotExist at once, which
		// is an answer and not a collision, and the LIVE path is the one under the collision.
		hits := collideUntilRead(t, ResultPath(jobDir), report())

		out := mustTriage(t, TriageInput{Pool: p})
		if !strings.Contains(out, "TRIAGE REPORT id="+sc.ID) {
			t.Errorf("a live RESULT.md that could not be read for an instant was not folded:\n%s", out)
		}
		if strings.Contains(out, "no_result=1") {
			t.Errorf("a collision was counted as a job that published nothing:\n%s", out)
		}
		if hits.Load() == 0 {
			t.Error("no read of this live report ever went through the collision wait")
		}
	})

	t.Run("the rehash collides", func(t *testing.T) {
		dir := t.TempDir()
		p, id := revisionPool(t, dir)
		copyPath := filepath.Join(p.ReportsDir(id), CopiedResult)

		// RULE 16's own window -- between the first hash and the rehash -- entered by a
		// COLLISION rather than by a writer: the path is replaced under the reader and the
		// bytes that come back are the SAME bytes. Nothing changed, so nothing may be
		// skipped, and the fixture is the same injected pause demanded test 16 uses.
		var hits *atomic.Int64
		out := mustTriage(t, TriageInput{Pool: p, pauseAfterFirstHash: func() {
			hits = collideUntilRead(t, copyPath, report())
		}})
		if strings.Contains(out, "TRIAGE SKIPPED id="+id) {
			t.Errorf("a rehash that COLLIDED was reported as a report that changed while it was read:\n%s", out)
		}
		if !strings.Contains(out, "TRIAGE REPORT id="+id) {
			t.Errorf("a report whose rehash collided was not folded:\n%s", out)
		}
		if hits == nil || hits.Load() == 0 {
			t.Error("no rehash of this report ever went through the collision wait")
		}
	})
}

// mustTriage runs one triage and hands back its stdout, so a test asserts on the lines a
// person reads and fails on the exit code where it happens.
func mustTriage(t *testing.T, in TriageInput) string {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if code := Triage(in); code != 0 {
		t.Fatalf("triage exited %d: %s%s", code, out.String(), errb.String())
	}
	return out.String()
}
