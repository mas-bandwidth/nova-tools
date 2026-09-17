package swarm

import (
	"bytes"
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
	r := &running{slot: 1, nonce: "abc123", exitAttest: ExitAttestHash(fixtureAttest), jobDir: jobDir, started: now, deadline: 30 * time.Second}

	alive, _ := in.state(r, now)
	if !alive {
		t.Fatal("a slot file that could not be READ is not a job that is OVER: the dispatcher finalized one on it")
	}

	// The observable that ends the wait: the supervisor's own completion evidence, under
	// this launch's nonce. It is read from the JOB's directory, which needs no slot file.
	if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: 0, End: EndDone, Nonce: r.nonce, Attest: fixtureAttest}); err != nil {
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

// THE WINDOWS FLAKE CLASS, finish side (run 34698330796, `TestANumericBudgetWithNoUsageSourceIsRefused`).
//
// A slot file that collided is not a slot the dispatcher cannot free. `finish` retired the
// slot when the slot file read failed, even when exit.json confirmed the job ended properly
// (nonce + attestation match). A retired slot makes the run exit 1 over a pool that drained
// green. The slot must be freed when exit.json is this launch's answer, because what the
// dispatcher could not read it cannot say is free -- but what the supervisor wrote beside
// the job is free enough.
func TestAFinishedJobFreesItsSlotWhenExitJSONConfirmsDespiteAnUnreadableSlotFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A slot file that is a DIRECTORY: every read fails, standing in for the Windows
	// collision.
	if err := os.MkdirAll(p.Path(Slots, "1.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(dir, "job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The job's own completion evidence, under this launch's nonce and attestation.
	nonce := "abc123"
	attestSecret := fixtureAttest
	if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: 0, End: EndDone, Nonce: nonce, Attest: attestSecret}); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(ResultPath(jobDir), []byte("# t\n\n## Head\nfindings: 0\nnotes read: 0\nrepo: o/n\nrev: abc\na paragraph.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := Sidecar{ID: "test-job-1", Files: 1, Tokens: 100000, Unmetered: true, Job: jobDir, Slot: 1, Started: Stamp(time.Now().UTC())}
	if err := p.WriteSidecar(Running, sc); err != nil {
		t.Fatal(err)
	}

	r := &running{sc: sc, slot: 1, nonce: nonce, exitAttest: ExitAttestHash(attestSecret), jobDir: jobDir, started: time.Now(), deadline: 30 * time.Second}

	var out, errb bytes.Buffer
	in := RunInput{Pool: p, Worker: Worker{}, Stdout: &out, Stderr: &errb, Now: func() time.Time { return time.Now().UTC() }}
	retired := map[int]bool{}

	_, end, _ := in.finish(r, retired, in.Now())
	if end != EndDone {
		t.Fatalf("the job ended done, got %q: %s%s", end, out.String(), errb.String())
	}
	if retired[1] {
		t.Fatal("a job whose exit.json confirms the end frees its slot: the dispatcher retired it over a collision")
	}
	// The slot is freed: no file remains.
	if _, err := os.Stat(p.slotPath(1)); err == nil {
		t.Error("the slot file is freed after finish confirms the job")
	}
}
