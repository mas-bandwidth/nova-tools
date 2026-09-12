package swarm

import (
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

	// collideUntilRead makes every read of `path` fail, and lifts the collision once the
	// reader has actually hit it: the wait is bounded by the number of polls, not by a
	// sleep, so this test cannot be slow and cannot miss. After the lift the platform's own
	// rule decides again -- false everywhere but Windows -- so nothing here loops twice.
	collideUntilRead := func(t *testing.T, path string, body string) *atomic.Int64 {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		var hits atomic.Int64
		var lifted atomic.Bool
		// The seam is scoped to THIS path by the error's own PathError, so every other
		// record this pass reads keeps the platform's rule (false off Windows) and no
		// other read in the test loops twice.
		forceTransientIO = func(err error) bool {
			var pe *fs.PathError
			if err == nil || lifted.Load() || !errors.As(err, &pe) || pe.Path != path {
				return transientIO(err)
			}
			if errors.Is(err, fs.ErrNotExist) {
				// The instant between the directory going and the file arriving is part of
				// the replace, and on Windows it is the same collision: while this fixture
				// is replacing the path, "not there yet" is not an answer either.
				return true
			}
			hits.Add(1)
			return true
		}
		t.Cleanup(func() { forceTransientIO = nil })
		go func() {
			deadline := time.Now().Add(SteadyWindow / 2)
			for hits.Load() < 3 && time.Now().Before(deadline) {
				time.Sleep(steadyPoll)
			}
			_ = os.Remove(path)
			_ = os.WriteFile(path, []byte(body), 0o644)
			lifted.Store(true)
		}()
		return &hits
	}

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
