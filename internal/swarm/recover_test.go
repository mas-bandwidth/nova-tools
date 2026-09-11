package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RULE 17'S RECLAIM MOVES THE JOB (SPEC-SWARM.md:372: "`finalize` runs for the job, its
// files move as rule 12 says, and the slot is freed").
//
// DeepSeek's confirming read of #60, finding 1: the start-up pass finalized a dead
// dispatcher's job and freed its slot but never moved the task, so every RECOVERED job sat
// in running/ forever -- invisible to done/ and failed/, counted running by `status`, and
// nothing ever looking at it again. The whole point of rule 17 is the recovery, and the
// recovery leaked.
func TestARecoveredJobMovesOutOfRunning(t *testing.T) {
	for _, c := range []struct {
		name, end, dest string
		exit            *ExitRecord
	}{
		{"a job whose supervisor recorded its exit", EndDone, Done, &ExitRecord{RC: 0, End: EndDone}},
		{"a job whose supervisor left no evidence", EndUnknown, Failed, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p, w := recoveryPool(t, dir)
			id := NewID(time.Now().UTC(), "recovered")
			jobDir := w.JobDir(1, id)
			if err := os.MkdirAll(jobDir, 0o755); err != nil {
				t.Fatal(err)
			}
			sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1, Started: Stamp(time.Now().UTC())}
			if err := p.Add([]byte("a task a dead dispatcher was running"), sc); err != nil {
				t.Fatal(err)
			}
			if err := p.Claim(id, Pending, Running); err != nil {
				t.Fatal(err)
			}
			if err := writeAtomic(ResultPath(jobDir), []byte(recoveredReport), 0o644); err != nil {
				t.Fatal(err)
			}
			nonce := "0123456789abcdef"
			if c.exit != nil {
				c.exit.Nonce = nonce
				if err := WriteJSON(ExitPath(jobDir), c.exit); err != nil {
					t.Fatal(err)
				}
			}
			// The dead dispatcher's slot file: a launched slot whose pid is gone. Pid 0 is
			// alive to nobody, which is what a dead leader looks like from here.
			if err := writeSlot(p.slotPath(1), SlotFile{
				Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
				PidStarted: "-", RunnerPid: 0, Nonce: nonce, LaunchedAt: Stamp(time.Now().UTC()),
			}); err != nil {
				t.Fatal(err)
			}

			var out, errb bytes.Buffer
			code := Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
				Now: func() time.Time { return time.Now().UTC() }})
			if !strings.Contains(out.String(), "RUN RECLAIM slot=1 id="+id) {
				t.Fatalf("a dead dispatcher's ended job is RECLAIMED:\n%s%s", out.String(), errb.String())
			}
			if c.end == EndUnknown && code == 0 {
				t.Errorf("an outcome with no completion evidence is not a green run, got exit 0")
			}
			// ITS FILES MOVE. This is the finding: the usage file and the report copy were
			// written, the slot was freed, and the task stayed in running/ forever.
			if _, err := os.Stat(p.taskFile(Running, id)); err == nil {
				t.Errorf("a reclaimed job is still in running/, with no slot and nothing watching it")
			}
			if _, err := os.Stat(p.taskFile(c.dest, id)); err != nil {
				t.Errorf("a reclaimed job belongs in %s/: %v", c.dest, err)
			}
			moved, err := p.ReadSidecar(c.dest, id)
			if err != nil {
				t.Fatalf("the sidecar moves with the task: %v", err)
			}
			if moved.End != c.end {
				t.Errorf("the moved sidecar wants end=%s, got %q", c.end, moved.End)
			}
			if _, err := os.Stat(p.UsagePath(id)); err != nil {
				t.Errorf("the usage file outlives the job: %v", err)
			}
			if _, err := os.Stat(p.slotPath(1)); err == nil {
				t.Errorf("the slot is freed by the reclaim")
			}
		})
	}
}

const recoveredReport = "# a recovered job\n\n## Head\nfindings: 0\nrepo: o/n\nrev: abc\nit finished before its dispatcher died.\n\n" +
	"## Per item\n| item | state | evidence |\n| --- | --- | --- |\n| an item | green | x.go:1 |\n"

func recoveryPool(t *testing.T, dir string) (*Pool, Worker) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "pool"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPool(filepath.Join(dir, "pool"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "worker-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return p, Worker{Name: "recovery", Provider: "fake", Model: "fake-model", Harness: "fake-harness",
		WorkerDir: home, Deadline: "30s", Usage: UsageOpenCode}
}

// RULE 7, VERBATIM (SPEC-SWARM.md:109-111): "A worker silent past its deadline is reaped
// and its job is re-queued once, with `requeued=1` in the new task's sidecar; a job reaped
// a second time goes to `failed/` with `reaped=2` and is not re-queued again."
//
// DeepSeek's read 4, finding 1: the rule held on the live path and was dropped on the
// recovery one. A supervisor that reaped its worker at the deadline while the dispatcher
// was dead left `end=killed` on exit.json; the next dispatcher finalized it straight into
// failed/ with a stale `reaped` and never ran the one retry. The rule is about the JOB, not
// about which dispatcher was alive to see it.
func TestARecoveredKilledJobRunsOnceMore(t *testing.T) {
	for _, c := range []struct {
		name         string
		wasReaped    int
		wantReaped   int
		wantRequeued bool
	}{
		{"the first reap runs once more", 0, 1, true},
		{"the second reap is final", 1, 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p, w := recoveryPool(t, dir)
			id := NewID(time.Now().UTC(), "reaped")
			jobDir := w.JobDir(1, id)
			if err := os.MkdirAll(jobDir, 0o755); err != nil {
				t.Fatal(err)
			}
			sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1,
				Reaped: c.wasReaped, Started: Stamp(time.Now().UTC())}
			if err := p.Add([]byte("a task a dead dispatcher's supervisor reaped"), sc); err != nil {
				t.Fatal(err)
			}
			if err := p.Claim(id, Pending, Running); err != nil {
				t.Fatal(err)
			}
			nonce := "0123456789abcdef"
			if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: -1, End: EndKilled, Nonce: nonce}); err != nil {
				t.Fatal(err)
			}
			if err := writeSlot(p.slotPath(1), SlotFile{
				Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
				PidStarted: "-", RunnerPid: 0, Nonce: nonce, LaunchedAt: Stamp(time.Now().UTC()),
			}); err != nil {
				t.Fatal(err)
			}
			// The pool is asked to stop, so this pass recovers and then starts nothing:
			// what the re-queue put in pending/ is read here rather than run.
			if err := os.WriteFile(p.Path(StopFile), []byte("stop\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var out, errb bytes.Buffer
			Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
				Now: func() time.Time { return time.Now().UTC() }})
			stdout := out.String()
			want := "RUN RECLAIM slot=1 id=" + id + " end=killed dest=failed"
			if !strings.Contains(stdout, want) {
				t.Fatalf("a reaped job is recovered as killed:\n%s%s", stdout, errb.String())
			}
			if !strings.Contains(stdout, "requeued="+boolWord(c.wantRequeued)) {
				t.Errorf("the reclaim line wants requeued=%t:\n%s", c.wantRequeued, stdout)
			}
			// The counts are the truth about the pool: a job this pass ended is in them.
			if !strings.Contains(stdout, "killed=1") || !strings.Contains(stdout, "recovered=1") {
				t.Errorf("RUN OK counts the job it recovered:\n%s", stdout)
			}
			// THE DURABLE RECORD, which is what a person reads tomorrow.
			moved, err := p.ReadSidecar(Failed, id)
			if err != nil {
				t.Fatalf("a recovered killed job belongs in failed/: %v", err)
			}
			if moved.Reaped != c.wantReaped {
				t.Errorf("the moved sidecar wants reaped=%d, got %d", c.wantReaped, moved.Reaped)
			}
			// The one automatic retry: a NEW task carrying from=<old> and requeued=1.
			pending, err := p.List(Pending)
			if err != nil {
				t.Fatal(err)
			}
			if !c.wantRequeued {
				if len(pending) != 0 {
					t.Fatalf("a job reaped a second time is not re-queued again, got %d pending", len(pending))
				}
				return
			}
			if len(pending) != 1 {
				t.Fatalf("rule 7 re-queues the job once, got %d pending", len(pending))
			}
			next := pending[0]
			if next.From != id || next.Requeued != 1 || next.Reaped != c.wantReaped {
				t.Errorf("the new task wants from=%s requeued=1 reaped=%d, got from=%s requeued=%d reaped=%d",
					id, c.wantReaped, next.From, next.Requeued, next.Reaped)
			}
			if _, err := p.Text(Pending, next.ID); err != nil {
				t.Errorf("the new task's text is the old task's text: %v", err)
			}
		})
	}
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
