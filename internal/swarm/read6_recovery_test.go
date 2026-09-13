package swarm

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// RULE 11, VERBATIM (SPEC-SWARM.md:154-156): a job whose group has a survivor "is
// quarantined: it moves to `failed/` with `violation=background` in the sidecar, and
// `triage` does not count it."
//
// READ 6, FINDING 1: the rule held on the live path and was dropped on the recovery one.
// A supervisor that made rule 11's group check while the dispatcher was dead wrote
// `end=done survivors=1` on exit.json; the next dispatcher's reclaim branch read the
// record, ignored `Survivors`, and finalized the job into done/ with no `violation=`
// word -- so `triage` folded a quarantined job's findings into a coordinator's page. The
// identical exit record was quarantined when the dispatcher lived and counted when it
// died: the failure direction rule 11 exists to prevent.
func TestARecoveredBackgroundViolationIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	id := NewID(time.Now().UTC(), "survivor")
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1, Started: Stamp(time.Now().UTC())}
	if err := p.Add([]byte("a task whose worker left a process behind"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(id, Pending, Running); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(ResultPath(jobDir), []byte(recoveredReport), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce := "0123456789abcdef"
	// What the supervisor's own group check leaves behind (supervise.go endWith): the
	// harness exited 0, and a process of the job's group was still alive after it.
	if err := WriteJSON(ExitPath(jobDir), ExitRecord{RC: 0, End: EndDone, Survivors: 1, Nonce: nonce, Attest: fixtureAttest}); err != nil {
		t.Fatal(err)
	}
	if err := writeSlot(p.slotPath(1), SlotFile{
		Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
		PidStarted: "-", RunnerPid: 0, Nonce: nonce, ExitAttest: ExitAttestHash(fixtureAttest),
		LaunchedAt: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		// The RECOVERY path is what this test is about and it starts no worker; the
		// wall is the launch seam's and is proved in cmd/nova-swarm's own tests, so
		// this unit takes rule 11's loud workaround rather than needing a binary.
		NoSandbox: true,
		Now:       func() time.Time { return time.Now().UTC() }})
	stdout := out.String()
	if !strings.Contains(stdout, "RUN RECLAIM slot=1 id="+id+" end="+EndViolation+" dest="+Failed) {
		t.Errorf("a recovered job with a survivor is rule 11's violation, into failed/:\n%s%s", stdout, errb.String())
	}
	// THE DURABLE RECORD, which is the one `triage` reads.
	moved, err := p.ReadSidecar(Failed, id)
	if err != nil {
		t.Fatalf("a quarantined job belongs in failed/: %v", err)
	}
	if moved.Violation != "background" {
		t.Errorf("the moved sidecar wants violation=background, got %q", moved.Violation)
	}
	if moved.End != EndViolation {
		t.Errorf("the moved sidecar wants end=%s, got %q", EndViolation, moved.End)
	}
	// AND `triage` DOES NOT COUNT IT: the whole point of the word.
	if _, err := p.ReadSidecar(Done, id); err == nil {
		t.Errorf("a quarantined job is not in done/")
	}
	// The counts are the truth about the pool: it landed in failed/.
	if !strings.Contains(stdout, "failed=1") {
		t.Errorf("RUN OK counts the quarantined job where it landed:\n%s", stdout)
	}
}
