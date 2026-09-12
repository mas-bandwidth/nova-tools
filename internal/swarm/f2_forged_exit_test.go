package swarm

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// fixtureAttest is the secret the fixtures plant as the supervisor's own, so the completion
// evidence they write carries an attestation whose hash matches the slot file they plant
// beside it.
const fixtureAttest = "the-fixture-supervisor-secret"

// PACKET 2 FINDING 2 (issue #164): A WORKER'S OWN WRITING IS NEVER COMPLETION EVIDENCE.
//
// The job directory is the FIRST `--write` of the wall (sandbox.go), so every byte of it is
// the worker's to write: `RESULT.md`, `note`, and -- before this branch -- the two records
// this tool keeps beside the job, `pid` and `exit.json`. The launch nonce was written into
// `<job>/pid`, which put the one value rule 17 checks completion evidence against inside the
// worker's own write set; and the nonce also travels in the supervisor's argv (`--nonce`),
// which any process of the same user reads out of the process table, inside the wall
// included. So the nonce is a name, not a secret, and evidence that is accepted on a nonce
// alone is evidence a worker can forge:
//
//	the harness writes <job>/exit.json {rc:0, end:"done", nonce:<the launch's>}
//	the supervisor dies before endWith (rule 18's between-exit-and-exit-json boundary)
//	the next dispatcher's rule 17 pass reads that file as the supervisor's own word
//
// and a job whose outcome is UNKNOWN lands in done/ with `end=done`, which is the one thing
// SPEC-SWARM.md:384-389 says can never happen: "the pid is dead, the group is dead, and
// there is **no** `exit.json` -- the outcome is **unknown** ... the job is never `ok` or
// `clean`".
//
// The repair is not the nonce's secrecy -- it cannot be kept, and this test does not pretend
// it can: the forgery below is written with the TRUE nonce, however the worker came by it.
// The repair is that the completion evidence carries a per-launch attestation the supervisor
// mints in its own memory and never writes anywhere a worker can read, whose HASH lives in
// the slot file (a file in neither the read set nor the write set), and which reaches the
// job directory only in the record itself, after the job's whole process group is dead.
func TestAForgedExitRecordCannotUpgradeAnUnknownOutcome(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	id := NewID(time.Now().UTC(), "forged")
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1, Started: Stamp(time.Now().UTC())}
	if err := p.Add([]byte("a task whose supervisor died between the exit and the exit record"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(id, Pending, Running); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(ResultPath(jobDir), []byte(recoveredReport), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce, err := Nonce()
	if err != nil {
		t.Fatal(err)
	}
	// The supervisor's own records for a launch that got as far as the harness: the slot
	// file it identified into, whose pid is now dead (pid 0 is alive to nobody), and the
	// identity record beside the job.
	if err := writeSlot(p.slotPath(1), SlotFile{
		Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
		PidStarted: "-", RunnerPid: 0, Nonce: nonce, LaunchedAt: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(PidPath(jobDir), PidRecord{
		Job: id, Slot: 1, State: SlotLaunched, Pid: 0, Pgid: 0, PidStarted: "-",
		Started: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}

	// THE CONTROL, and the outcome the spec demands: a dead leader, a dead group and no
	// exit.json is `unknown`.
	if d := p.Decide(1); d.Kind != DecideUnknown {
		t.Fatalf("a dead launch with no exit.json is unknown, got kind=%s reason=%q", d.Kind, d.Reason)
	}

	// THE FORGERY. `end=done rc=0 survivors=0` is the best outcome this tool has, written
	// by the worker into its own write set under the launch's true nonce.
	if err := WriteJSON(ExitPath(jobDir), ExitRecord{
		RC: 0, End: EndDone, Survivors: 0, Nonce: nonce, Ended: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}
	if d := p.Decide(1); d.Kind == DecideReclaim {
		t.Errorf("a worker-written exit.json under the launch's nonce was read as the supervisor's own completion evidence: kind=%s reason=%q exit=%+v",
			d.Kind, d.Reason, d.Exit)
	}

	// AND THE WHOLE PASS: the run that decides this slot must not land the job in done/.
	var out, errb bytes.Buffer
	code := Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Now: func() time.Time { return time.Now().UTC() }})
	if strings.Contains(out.String(), "dest=done") {
		t.Errorf("the forged record upgraded an unknown outcome to done:\n%s%s", out.String(), errb.String())
	}
	if _, err := os.Stat(p.taskFile(Done, id)); err == nil {
		t.Errorf("the task is in done/ on a worker's own word")
	}
	if code == 0 {
		t.Errorf("a slot whose completion evidence is not its supervisor's is not a green run, got exit 0:\n%s%s", out.String(), errb.String())
	}
}

// AND THE NONCE IS OUT OF THE WORKER'S WRITE SET. `<job>/pid` is the identity record beside
// the job, inside the first `--write`; the nonce was written into it twice (supervise.go)
// and read back by nothing. The record keeps the pids and the start stamps, which are what
// its readers want (`readJobProc`, `FinalizeByHand`), and carries no launch nonce.
func TestTheIdentityRecordBesideTheJobCarriesNoLaunchNonce(t *testing.T) {
	raw, err := json.Marshal(PidRecord{Job: "job", Slot: 1, State: SlotLaunched, Pid: 2, Pgid: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "nonce") {
		t.Errorf("<job>/pid is inside the worker's write set and carries the launch nonce: %s", raw)
	}
}

// THE GREEN CONTRACT: a reclaim needs the launch's attestation, not only its nonce. The
// nonce is a name a worker can read (the supervisor's argv, the pid record, supervisor.log,
// aborted.json); the attestation is a secret the supervisor minted in its own memory, whose
// hash alone lives in the slot file (a file in neither sandbox list). So the three shapes of
// a nonce-matching exit.json are decided apart: an absent or wrong attestation is quarantine,
// and only a true attestation is the supervisor's own word and reclaims.
func TestAnExitRecordIsReclaimedOnlyWithTheLaunchAttestation(t *testing.T) {
	for _, c := range []struct {
		name   string
		attest string
		want   string
	}{
		{"absent attestation", "", DecideQuarantine},
		{"wrong attestation", "a-worker-forged-secret", DecideQuarantine},
		{"true attestation", fixtureAttest, DecideReclaim},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p, w := recoveryPool(t, dir)
			id := NewID(time.Now().UTC(), "attested")
			jobDir := w.JobDir(1, id)
			if err := os.MkdirAll(jobDir, 0o755); err != nil {
				t.Fatal(err)
			}
			sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1, Started: Stamp(time.Now().UTC())}
			if err := p.Add([]byte("a task whose evidence carries, or does not, the attestation"), sc); err != nil {
				t.Fatal(err)
			}
			if err := p.Claim(id, Pending, Running); err != nil {
				t.Fatal(err)
			}
			if err := writeAtomic(ResultPath(jobDir), []byte(recoveredReport), 0o644); err != nil {
				t.Fatal(err)
			}
			nonce, err := Nonce()
			if err != nil {
				t.Fatal(err)
			}
			if err := writeSlot(p.slotPath(1), SlotFile{
				Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
				PidStarted: "-", RunnerPid: 0, Nonce: nonce, ExitAttest: ExitAttestHash(fixtureAttest),
				LaunchedAt: Stamp(time.Now().UTC()),
			}); err != nil {
				t.Fatal(err)
			}
			if err := WriteJSON(ExitPath(jobDir), ExitRecord{
				RC: 0, End: EndDone, Survivors: 0, Nonce: nonce, Attest: c.attest,
				Ended: Stamp(time.Now().UTC()),
			}); err != nil {
				t.Fatal(err)
			}
			if d := p.Decide(1); d.Kind != c.want {
				t.Fatalf("%s: a nonce-matching exit.json decided %s (%q), want %s", c.name, d.Kind, d.Reason, c.want)
			}

			var out, errb bytes.Buffer
			Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
				NoSandbox: true, Now: func() time.Time { return time.Now().UTC() }})
			if c.want == DecideReclaim {
				if !strings.Contains(out.String(), "dest=done") {
					t.Errorf("%s: a true attestation reclaims into done/:\n%s%s", c.name, out.String(), errb.String())
				}
				return
			}
			if strings.Contains(out.String(), "done=1") {
				t.Errorf("%s: a nonce without the attestation must not print done=1:\n%s%s", c.name, out.String(), errb.String())
			}
		})
	}
}
