package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// THE WINDOWS FLAKE CLASS, as a rule that holds on every platform (run 34698330796,
// `TestANumericBudgetWithNoUsageSourceIsRefused`, and #92 before it).
//
// A read of a durable record that FAILED is not evidence about the job whose record it is.
// On Windows a read of a path whose old file is delete-pending fails for microseconds every
// time somebody replaces it -- and the supervisor replaces its own slot file, to record the
// job's process group, milliseconds after it identifies and exactly when the dispatcher's
// first poll of that file lands. `state` finalized on any error at all, so one such read
// reaped a LIVE supervisor at `after=0s` and then found no exit.json where it had just
// killed the process that writes it: `RUN DONE … rc=-1 … dest=failed` beside
// `result=ok findings=1`, and the pass exited 1.
//
// The platform's collision is waited out in fileretry.go. THIS is the rule underneath it,
// and it is asserted here with no Windows and no race: a slot file that cannot be read for
// a reason other than being GONE is an unanswered question, and the answer is looked for in
// an observable -- the supervisor's own completion evidence -- with the clock only as the
// outer bound.
func TestAnUnreadableSlotFileIsNotEvidenceAJobIsOver(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A slot file that is a DIRECTORY: every read of it fails, and none of those failures
	// says the file is gone. It is this package's portable stand-in for the microsecond a
	// Windows replace is pending.
	if err := os.MkdirAll(p.Path(Slots, "1.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(dir, "job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	in := RunInput{Pool: p}
	now := time.Now()
	r := &running{slot: 1, nonce: "abc123", jobDir: jobDir, started: now, deadline: 30 * time.Second}

	alive, _ := in.state(r, now)
	if !alive {
		t.Fatal("a slot file that could not be READ is not a job that is OVER: the dispatcher finalized one on it")
	}

	// The observable that ends the wait: the supervisor's own completion evidence, under
	// this launch's nonce. It is read from the JOB's directory, which needs no slot file.
	if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: 0, End: EndDone, Nonce: r.nonce}); err != nil {
		t.Fatal(err)
	}
	if alive, _ := in.state(r, now); alive {
		t.Error("exit.json under this launch's nonce is the supervisor's own word that the job is over")
	}

	// Somebody else's evidence is not this job's answer.
	if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: 0, End: EndDone, Nonce: "another-launch"}); err != nil {
		t.Fatal(err)
	}
	if alive, _ := in.state(r, now); !alive {
		t.Error("an exit.json from ANOTHER launch is not this job's completion evidence")
	}

	// THE OUTER BOUND. The wait ends on its own, like every wait in this repository: a
	// record that stays unreadable past this job's own deadline is finalized rather than
	// watched forever.
	late := now.Add(r.deadline + TerminateGrace*2 + time.Second)
	if alive, _ := in.state(r, late); alive {
		t.Error("a wait for a readable record has a deadline, and this one never ended")
	}

	// And a slot file that is GONE is an answer, not a collision: it is read at once.
	fresh := &running{slot: 2, nonce: "abc123", jobDir: jobDir, started: now, deadline: 30 * time.Second}
	if _, err := os.Stat(p.Path(Slots, strconv.Itoa(fresh.slot)+".json")); err == nil {
		t.Fatal("this half of the test wants a slot with no file")
	}
	if alive, _ := in.state(fresh, now); alive {
		t.Error("a slot file that is GONE is an answer, and the dispatcher waits on nothing for it")
	}
}
