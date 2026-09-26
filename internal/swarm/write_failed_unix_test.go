//go:build unix

package swarm

// Glenn's failure-guidance requirement (2026-09-26): no swarm state write
// fails silently. Every site the dispatcher or the supervisor wrote a
// sidecar, a claim, a record or a pid with `_ =` now reports the path it
// could not write. Each test here makes ONE directory unwritable at the
// moment the site writes into it -- through the clock seam the site reads
// just before, or before the call when nothing else touches the directory
// -- and reads the line off stderr.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// readOnly makes dirs unwritable for the rest of the test and restores them
// before t.TempDir removes them. Root writes through 0o555, so the fault
// cannot be injected there.
func readOnly(t *testing.T, dirs ...string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0o555 directory; the fault cannot be injected")
	}
	for _, d := range dirs {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(d, 0o755) })
	}
}

// onceClaimed is a clock that makes dirs read-only the first time it is read
// after the task is in running/: the dispatcher reads it between the claim
// and the write.
func onceClaimed(t *testing.T, p *Pool, id string, dirs ...string) func() time.Time {
	t.Helper()
	var once sync.Once
	return func() time.Time {
		if _, err := os.Stat(p.taskFile(Running, id)); err == nil {
			once.Do(func() { readOnly(t, dirs...) })
		}
		return time.Now().UTC()
	}
}

func writeFailedLine(id, what, path string) string {
	return "RUN WRITE-FAILED id=" + id + " what=" + what + " path=" + path + ": "
}

func claimFailedLine(p *Pool, id, from, to string) string {
	return "RUN WRITE-FAILED id=" + id + " what=claim path=" + p.taskFile(to, id) + " from=" + from + " to=" + to + ": "
}

func wantLines(t *testing.T, stderr string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(stderr, l) {
			t.Errorf("stderr lacks %q:\n%s", l, stderr)
		}
	}
}

func poolIdentity(t *testing.T, p *Pool) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.Dir, "identity.tsv"), []byte("owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run.go, the recovery pass: a recovered job's sidecar and its move out of
// running/ (rule 17) are said when they fail, with the path each one wanted.
func TestRecoveryReportsTheSidecarAndClaimItCouldNotWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	id := NewID(time.Now().UTC(), "recovered-ro")
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
	if err := WriteJSON(ExitPath(jobDir), &ExitRecord{RC: 0, End: EndDone, Attest: fixtureAttest, Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	if err := writeSlot(p.slotPath(1), SlotFile{
		Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
		PidStarted: "-", RunnerPid: 0, Nonce: nonce, ExitAttest: ExitAttestHash(fixtureAttest),
		LaunchedAt: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}
	readOnly(t, p.Path(Running))
	var out, errb strings.Builder
	Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Now: func() time.Time { return time.Now().UTC() }})
	wantLines(t, errb.String(),
		writeFailedLine(id, "sidecar", p.sidecarFile(Running, id)),
		claimFailedLine(p, id, Running, Done)+"")
	if !strings.Contains(errb.String(), "the task stays in running/") {
		t.Errorf("the claim line does not say where the task is:\n%s", errb.String())
	}
	if _, err := os.Stat(p.taskFile(Running, id)); err != nil {
		t.Errorf("the task is not where the line says: %v", err)
	}
}

// run.go, the public-class gate: a CARD REFUSED whose sidecar and move to
// failed/ cannot be written says so, instead of a refused card sitting in
// running/ as if it ran.
func TestARefusedPublicCardReportsTheWritesItCouldNotMake(t *testing.T) {
	t.Parallel()
	p, w := recoveryPool(t, t.TempDir())
	w.Class = WorkerClassPublic // no public-repos.txt in the pool: every card is refused
	id := NewID(time.Now().UTC(), "public-ro")
	if err := p.Add([]byte("RESULT: a card\nSTEP 1. do it\n"), Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1}); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Now: onceClaimed(t, p, id, p.Path(Running))})
	wantLines(t, errb.String(), "CARD REFUSED reason=private-source",
		writeFailedLine(id, "sidecar", p.sidecarFile(Running, id)),
		claimFailedLine(p, id, Running, Failed))
}

// run.go, the slot lease: a task handed back to pending/ because the store
// could not be read, or because the take was refused, says when the hand-back
// itself failed -- the task is still in running/ with no slot.
func TestASlotRefusalReportsTheClaimBackItCouldNotMake(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		store func(t *testing.T) string
		line  string
	}{
		{"a store that is not there", func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }, "RUN REFUSED reason=slots"},
		{"a store with no seat free", func(t *testing.T) string {
			store := writeSlotStore(t, "capacity\t1\nreserve\t0\nbench\t1\nother\t1\n")
			if err := MakeSlotLease(store, "other-1", "other", os.Getpid(), "busy", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			return store
		}, "RUN WAIT slots owner=bench"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, w := recoveryPool(t, t.TempDir())
			id := NewID(time.Now().UTC(), "slot-ro")
			if err := p.Add([]byte("RESULT: a card\nSTEP 1. do it\n"), Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Deadline: "1ms"}); err != nil {
				t.Fatal(err)
			}
			var out, errb strings.Builder
			Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
				NoSandbox: true, Now: onceClaimed(t, p, id, p.Path(Running)),
				SlotsStore: c.store(t), SlotOwner: "bench", SlotPoll: time.Millisecond})
			wantLines(t, out.String()+errb.String(), c.line)
			wantLines(t, errb.String(), claimFailedLine(p, id, Running, Pending))
		})
	}
}

// run.go, the route seam: a card the ladder parks writes three records into
// routed-out/, and each one that cannot be written is named.
func TestAParkedCardReportsTheWritesItCouldNotMake(t *testing.T) {
	t.Parallel()
	p, w := recoveryPool(t, t.TempDir())
	body := "RESULT: CARD-7 nova-tools #1486 parked, not run\nKIND: fix-with-red-test\nFILES: 4\nPACKAGES: 1\nSTEP 1. do the thing\n"
	id := NewID(time.Now().UTC(), "parked-ro")
	if err := p.Add([]byte(body), Sidecar{ID: id, Label: "asked-card", Files: 4, Tokens: 1000, RC: -1}); err != nil {
		t.Fatal(err)
	}
	routeDir := t.TempDir()
	var once sync.Once
	var out, errb strings.Builder
	Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Now: func() time.Time { return time.Now().UTC() },
		Route: &RouteInput{Registry: routeTestReg(t), Floor: 0.9,
			// The route's clock is read when its decision is logged, after the claim and
			// before the park: the moment both directories go read-only.
			Now: func() time.Time {
				once.Do(func() { readOnly(t, p.Path(Running), p.Path(RoutedOut)) })
				return routeTestNow()
			},
			Log:   filepath.Join(routeDir, "decide.jsonl"),
			Usage: filepath.Join(routeDir, "usage.tsv"),
			Decide: func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
				return nil, decide.Usage{}, fmt.Errorf("no provider should be asked when one rung is eligible")
			}}})
	wantLines(t, errb.String(),
		writeFailedLine(id, "sidecar", p.sidecarFile(Running, id)),
		claimFailedLine(p, id, Running, RoutedOut),
		writeFailedLine(id, "route", p.Path(RoutedOut, id+".route")))
}

// launchFixture is a pool a launch can run in: an identity row, one pending
// task claimed into running/, a supervisor that exits at once, and the
// worker's home.
func launchFixture(t *testing.T, sc Sidecar) (RunInput, *Pool, Worker, *strings.Builder) {
	t.Helper()
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	poolIdentity(t, p)
	if err := p.Add([]byte("RESULT: a card\nSTEP 1. do it\n"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(sc.ID, Pending, Running); err != nil {
		t.Fatal(err)
	}
	supervisor := filepath.Join(dir, "supervisor")
	if err := os.WriteFile(supervisor, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	errb := &strings.Builder{}
	in := RunInput{Pool: p, Worker: w, Workers: 1, Stdout: &strings.Builder{}, Stderr: errb, NoSandbox: true,
		Supervisor: supervisor, WorkerFile: filepath.Join(dir, "worker.json"), LaunchTimeout: 5 * time.Second,
		Now: func() time.Time { return time.Now().UTC() }}
	return in, p, w, errb
}

// run.go, the launch: the sidecar after the job is assigned, the launch
// record, the supervisor's pid, and the sidecar, move and slot after a
// supervisor that died without identifying itself -- every one names its
// path when it cannot be written, and the launch still ends LAUNCH-FAILED.
func TestALaunchReportsEveryRecordItCouldNotWrite(t *testing.T) {
	t.Parallel()
	id := NewID(time.Now().UTC(), "launch-ro")
	in, p, w, errb := launchFixture(t, Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1})
	evidence := p.Path("evidence")
	if err := os.MkdirAll(evidence, 0o755); err != nil {
		t.Fatal(err)
	}
	// The pid file's path is a directory, so the write cannot land; the job
	// directory itself stays writable for the supervisor log.
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(filepath.Join(jobDir, "supervisor.pid"), 0o755); err != nil {
		t.Fatal(err)
	}
	readOnly(t, p.Path(Running), evidence)
	r, line, code := in.launch(Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1}, []byte("RESULT: a card\nSTEP 1. do it\n"), 0, nil, nil)
	if r != nil || code != launchBroken || !strings.HasPrefix(line, "RUN LAUNCH-FAILED id="+id+" slot=1 after=") {
		t.Fatalf("launch = %v %q %d, want LAUNCH-FAILED", r, line, code)
	}
	if !strings.Contains(line, "the supervisor exited without writing an identity") {
		t.Errorf("the line does not say what the supervisor did: %s", line)
	}
	wantLines(t, errb.String(),
		writeFailedLine(id, "sidecar", p.sidecarFile(Running, id)),
		"RUN WRITE-FAILED id="+id+" what=launch-record path="+filepath.Join(evidence, id, "launch")+"/",
		writeFailedLine(id, "pid", filepath.Join(jobDir, "supervisor.pid")),
		claimFailedLine(p, id, Running, Failed))
	if got := strings.Count(errb.String(), writeFailedLine(id, "sidecar", p.sidecarFile(Running, id))); got != 2 {
		t.Errorf("the sidecar is written before the launch and after the failed handshake; %d lines, want 2:\n%s", got, errb.String())
	}
	if _, err := os.Stat(p.slotPath(1)); err == nil {
		t.Error("the slot is freed even though the task's records could not be written")
	}
}

// run.go, the supervisor log: a job whose supervisor.log cannot be opened is
// not launched. Before this the supervisor ran with its output on /dev/null.
func TestALaunchWhoseSupervisorLogCannotOpenIsLaunchFailed(t *testing.T) {
	t.Parallel()
	id := NewID(time.Now().UTC(), "log-ro")
	in, p, w, errb := launchFixture(t, Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1})
	jobDir := w.JobDir(1, id)
	var once sync.Once
	// The clock is read once the job directory is prepared and before the
	// log is opened: the moment the job directory goes read-only.
	in.Now = func() time.Time {
		if _, err := os.Stat(jobDir); err == nil {
			once.Do(func() { readOnly(t, jobDir) })
		}
		return time.Now().UTC()
	}
	r, line, code := in.launch(Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1}, []byte("RESULT: a card\nSTEP 1. do it\n"), 0, nil, nil)
	want := "RUN LAUNCH-FAILED id=" + id + " slot=1 after=0s: the supervisor log " + filepath.Join(jobDir, "supervisor.log") + " could not be opened: "
	if r != nil || code != launchBroken || !strings.HasPrefix(line, want) {
		t.Fatalf("launch = %v %q %d, want %q", r, line, code, want)
	}
	if strings.Contains(errb.String(), "RUN WRITE-FAILED") {
		t.Errorf("nothing else failed to write:\n%s", errb.String())
	}
	if _, err := os.Stat(p.slotPath(1)); err == nil {
		t.Error("the slot is freed when the job is not launched")
	}
	if _, err := os.Stat(filepath.Join(jobDir, "supervisor.pid")); err == nil {
		t.Error("a supervisor was started with its output on /dev/null")
	}
}

// run.go, the input limit: a task refused before its launch reports the
// sidecar and the move to failed/ it could not write.
func TestAnInputLimitRefusalReportsTheWritesItCouldNotMake(t *testing.T) {
	t.Parallel()
	id := NewID(time.Now().UTC(), "limit-ro")
	sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, MaxInput: 1}
	in, p, _, errb := launchFixture(t, sc)
	readOnly(t, p.Path(Running))
	r, line, code := in.launch(sc, []byte("RESULT: a card\nSTEP 1. do it\n"), 0, nil, nil)
	if r != nil || code != launchRefused || !strings.HasPrefix(line, "RUN INPUT-LIMIT id="+id) {
		t.Fatalf("launch = %v %q %d, want INPUT-LIMIT", r, line, code)
	}
	wantLines(t, errb.String(),
		writeFailedLine(id, "sidecar", p.sidecarFile(Running, id)),
		claimFailedLine(p, id, Running, Failed))
}

// pool.go, Claim: a task that moved while its sidecar could not follow is
// reported to the caller as *SidecarStayed naming the record's path; the
// claim stands, ClaimNext still claims, and the run writes it down.
func TestClaimReportsASidecarThatStayed(t *testing.T) {
	t.Parallel()
	p, w := recoveryPool(t, t.TempDir())
	for _, id := range []string{NewID(time.Now().UTC(), "stayed-a"), NewID(time.Now().UTC(), "stayed-b")} {
		if err := p.Add([]byte("RESULT: a card\nSTEP 1. do it\n"), Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1}); err != nil {
			t.Fatal(err)
		}
		// The sidecar's destination is a directory with something in it: the
		// rename cannot land there.
		if err := os.MkdirAll(filepath.Join(p.sidecarFile(Running, id), "x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := p.List(Pending)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %v %v", pending, err)
	}
	first := pending[0].ID
	err = p.Claim(first, Pending, Running)
	var stayed *SidecarStayed
	if !errorsAs(err, &stayed) || stayed.ID != first || stayed.Path != p.sidecarFile(Pending, first) || stayed.To != Running {
		t.Fatalf("Claim = %v, want *SidecarStayed naming %s", err, p.sidecarFile(Pending, first))
	}
	if _, serr := os.Stat(p.taskFile(Running, first)); serr != nil {
		t.Fatalf("the claim stands: %v", serr)
	}
	second := pending[1].ID
	sc, _, claimed, err := p.ClaimNext()
	if !claimed || sc.ID != second || !errorsAs(err, &stayed) {
		t.Fatalf("ClaimNext = %s %v %v, want the claim and the report", sc.ID, claimed, err)
	}
	if err := os.Rename(p.taskFile(Running, second), p.taskFile(Pending, second)); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Now: func() time.Time { return time.Now().UTC() }})
	wantLines(t, errb.String(), writeFailedLine(second, "sidecar-move", p.sidecarFile(Pending, second)))
}

// supervise.go, after the harness starts: the pid record and the slot file
// carry the harness's group, and a write of either that fails is on the
// supervisor's own log with its path. The two directories go read-only at
// the afterStart seam, the instant between the start and the writes (the
// writes come microseconds after the start, so nothing outside the process
// can land a fault there).
func TestSuperviseReportsThePidAndSlotRecordsItCouldNotWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	id := NewID(time.Now().UTC(), "supervise-ro")
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness")
	if err := os.WriteFile(harness, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.Harness = harness
	nonce := "0123456789abcdef"
	slot, err := p.claimFree(1, nil, id, nonce, os.Getpid(), time.Now().UTC(), func(int) string { return jobDir })
	if err != nil || slot != 1 {
		t.Fatalf("claimFree = %d %v", slot, err)
	}
	var out, errb strings.Builder
	Supervise(SuperviseInput{Pool: p, Task: id, Slot: 1, Nonce: nonce, Worker: w,
		Sidecar: Sidecar{ID: id, Slot: 1, Job: jobDir, Deadline: "30s"},
		Stdout:  &out, Stderr: &errb, Now: func() time.Time { return time.Now().UTC() }, Sleep: func(time.Duration) {},
		afterStart: func() { readOnly(t, jobDir, p.Path(Slots)) }})
	wantLines(t, errb.String(),
		"SUPERVISE WRITE-FAILED slot=1 id="+id+" what=pid path="+PidPath(jobDir)+": ",
		"SUPERVISE WRITE-FAILED slot=1 id="+id+" what=slot path="+p.slotPath(1)+": ")
}

func errorsAs(err error, target **SidecarStayed) bool {
	for e := err; e != nil; {
		if s, ok := e.(*SidecarStayed); ok {
			*target = s
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
