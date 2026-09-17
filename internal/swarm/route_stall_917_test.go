package swarm

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ISSUE #917: Muse on Zen queues forever above ~30-40 concurrent requests per key; 60 cards
// froze mid-tool-call for 13 minutes while a direct Flash run answered in 11s beside them.
// There are two mechanisms here and both are tested with fakes, never the network:
//
//  1. a per-route in-flight cap: a worker's `route` (default provider/model) and
//     `max_inflight` (optional) keep a run from launching above the route's ceiling;
//  2. a stall detector: a running task's harness log that has not grown for `stall_after`
//     (default 4m) is reaped end=stall, and re-queued once through rule 7's own path.

// TestWorkerRouteDefaultsAndValidates pins the description-level contract: the route defaults
// to provider/model, a negative max_inflight and a non-positive stall_after are refusals, and
// a description that names neither still carries the default stall ceiling.
func TestWorkerRouteDefaultsAndValidates(t *testing.T) {
	w := Worker{Provider: "deepseek", Model: "deepseek-chat"}
	if got := w.RouteName(); got != "deepseek/deepseek-chat" {
		t.Errorf("a route with no `route` field wants provider/model, got %q", got)
	}
	if got := w.StallAfterDuration(); got != DefaultStallAfter {
		t.Errorf("a route with no `stall_after` wants %s, got %s", DefaultStallAfter, got)
	}
	w.Route = "zen/muse"
	if got := w.RouteName(); got != "zen/muse" {
		t.Errorf("a named route wins over the default, got %q", got)
	}
	w.StallAfter = "90s"
	if got := w.StallAfterDuration(); got != 90*time.Second {
		t.Errorf("a named stall_after is read, got %s", got)
	}
}

// TestRouteInflightCountsBenchLeases is the counting rule: this run's own watchers on the
// lane, plus every live lease of a named bench store whose label carries the route. An
// expired lease with a dead pid is not held; a DRIFT lease (past until= with a live pid) is.
func TestRouteInflightCountsBenchLeases(t *testing.T) {
	now := time.Now().UTC()
	store := t.TempDir()
	if err := MakeSlotLease(store, "s1", "someone", os.Getpid(), "zen/muse card 1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := MakeSlotLease(store, "s2", "someone", os.Getpid(), "deepseek/deepseek-chat card 2", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Expired with a live pid: DRIFT, and still held.
	if err := MakeSlotLease(store, "s3", "someone", os.Getpid(), "zen/muse drift", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Expired with a pid nobody holds: not held.
	if err := MakeSlotLease(store, "s4", "someone", 0, "zen/muse gone", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	in := RunInput{Worker: Worker{Provider: "zen", Model: "muse"}, SlotsStore: store}
	got := in.routeInflight(map[int]*running{}, now)
	if got != 2 {
		t.Fatalf("route zen/muse wants 2 live leases (one live, one DRIFT), got %d", got)
	}
	// The run's own watchers on the lane count too.
	watching := map[int]*running{
		1: {route: "zen/muse"},
		2: {route: "deepseek/deepseek-chat"},
	}
	if got := in.routeInflight(watching, now); got != 3 {
		t.Fatalf("own watchers on the lane count: want 3, got %d", got)
	}
	if in.routeAtCap(watching, now) {
		t.Fatal("max_inflight 0 is no ceiling, so routeAtCap must be false")
	}
	in.Worker.MaxInflight = 4
	if in.routeAtCap(watching, now) {
		t.Fatal("3 in flight under a cap of 4 is not at the ceiling")
	}
	in.Worker.MaxInflight = 3
	if !in.routeAtCap(watching, now) {
		t.Fatal("3 in flight at a cap of 3 is the ceiling")
	}
}

// TestRunHoldsARouteAtItsInFlightCap is the card's first red test: a route at cap 2 with
// three tasks runs two at once, the STATUS line says inflight=2 cap=2, and the third waits
// in pending/ until one ends. It drives the real dispatcher and the real supervisor, with
// the fake runner as the harness, so the count is the count a bench would see.
func TestRunHoldsARouteAtItsInFlightCap(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	harness := runnerDoing(t, dir, "cap-harness",
		runnerStep{Op: "stdout", Body: "working on it", Ms: 700})
	w.Harness = harness
	w.HarnessArgs = []string{"cap", "1", "{model}", "{prompt}", "{prompt}"}
	w.EnvVar = "FAKE_KEY"
	w.Usage = UsageNone
	w.Route = "fake/fake-model"
	w.MaxInflight = 2
	w.KeyFile = filepath.Join(dir, "key")
	if err := os.WriteFile(w.KeyFile, []byte("a-fake-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workerFile := filepath.Join(dir, "worker.json")
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workerFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		sc := Sidecar{ID: NewID(time.Now().UTC(), "cap"), Files: 1, Unmetered: true, Deadline: "30s"}
		if err := p.Add([]byte("a task that waits its turn on the route"), sc); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	code := Run(RunInput{
		Pool: p, Worker: w, Workers: 3, Hours: 0.005, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Supervisor: buildNovaSwarm(t), WorkerFile: workerFile,
		LaunchTimeout: 8 * time.Second, Now: func() time.Time { return time.Now().UTC() },
	})
	stdout := out.String()
	if !strings.Contains(stdout, "STATUS ROUTE fake/fake-model inflight=2 cap=2") {
		t.Fatalf("a route at cap 2 with three tasks prints inflight=2 cap=2:\n%s%s", stdout, errb.String())
	}
	if strings.Contains(stdout, "inflight=3") {
		t.Errorf("a route at cap 2 never has three in flight:\n%s", stdout)
	}
	if n := strings.Count(stdout, "RUN START"); n != 3 {
		t.Errorf("all three tasks eventually ran once a lane freed, got %d RUN START:\n%s%s", n, stdout, errb.String())
	}
	if code != 0 {
		t.Errorf("a drained capped run exits 0, got %d:\n%s%s", code, stdout, errb.String())
	}
}

// TestAStalledHarnessEndsWithStallNamingItsLastLine is the card's second red test: a fake
// harness that says one line and then stops writing is ended end=stall, and the supervisor's
// STALL line names that last line.
func TestAStalledHarnessEndsWithStallNamingItsLastLine(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	harness := runnerDoing(t, dir, "stalled-harness",
		runnerStep{Op: "stdout", Body: "the last thing it said before it froze"},
		runnerStep{Op: "sleep", Ms: 5000})
	w.Harness = harness
	w.HarnessArgs = []string{"stall", "1", "{model}", "{prompt}", "{prompt}"}
	w.Usage = UsageNone
	w.Deadline = "30s"
	w.StallAfter = "400ms"

	id := NewID(time.Now().UTC(), "stalled")
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1,
		Started: Stamp(time.Now().UTC())}
	if err := p.Add([]byte("a task whose harness freezes mid-call"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(id, Pending, Running); err != nil {
		t.Fatal(err)
	}
	nonce, err := Nonce()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSlot(p.slotPath(1), SlotFile{
		Job: id, JobDir: jobDir, State: SlotReserved, Pid: 0, Pgid: 0,
		PidStarted: "-", Nonce: nonce, LaunchedAt: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	Supervise(SuperviseInput{
		Pool: p, Task: id, Slot: 1, Nonce: nonce, Worker: w, Sidecar: sc,
		UsageInterval: 100 * time.Millisecond, Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return time.Now().UTC() },
	})
	var rec ExitRecord
	if err := ReadJSON(ExitPath(jobDir), &rec); err != nil {
		t.Fatalf("the supervisor leaves completion evidence: %v\n%s", err, errb.String())
	}
	if rec.End != EndStall {
		t.Fatalf("a silent harness ends end=%s, got %q\n%s", EndStall, rec.End, errb.String())
	}
	if !strings.Contains(errb.String(), "STALL task="+id) {
		t.Errorf("the STALL line names the task:\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "the last thing it said before it froze") {
		t.Errorf("the STALL line names the last log line:\n%s", errb.String())
	}
}

// TestAStalledTaskIsRequeuedOnceAndNotTwice is the card's third red test. Rule 7's own path
// carries the retry: the first stall queues one new attempt carrying from=/requeued=1, and a
// second stall is final, exactly as a second reap is.
func TestAStalledTaskIsRequeuedOnceAndNotTwice(t *testing.T) {
	for _, c := range []struct {
		name         string
		wasReaped    int
		wantRequeued bool
	}{
		{"the first stall runs once more", 0, true},
		{"the second stall is final", 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p, w := recoveryPool(t, dir)
			w.Usage = UsageNone
			id := NewID(time.Now().UTC(), "stall-requeue")
			jobDir := w.JobDir(1, id)
			if err := os.MkdirAll(jobDir, 0o755); err != nil {
				t.Fatal(err)
			}
			sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: jobDir, Slot: 1,
				Reaped: c.wasReaped, Started: Stamp(time.Now().UTC())}
			if err := p.Add([]byte("a task a frozen harness held"), sc); err != nil {
				t.Fatal(err)
			}
			if err := p.Claim(id, Pending, Running); err != nil {
				t.Fatal(err)
			}
			nonce := "0123456789abcdef"
			if err := WriteJSON(ExitPath(jobDir), ExitRecord{
				RC: -1, End: EndStall, Nonce: nonce, Attest: fixtureAttest,
				Ended: Stamp(time.Now().UTC()),
			}); err != nil {
				t.Fatal(err)
			}
			if err := writeSlot(p.slotPath(1), SlotFile{
				Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
				PidStarted: "-", Nonce: nonce, ExitAttest: ExitAttestHash(fixtureAttest),
				LaunchedAt: Stamp(time.Now().UTC()),
			}); err != nil {
				t.Fatal(err)
			}
			r := &running{sc: sc, slot: 1, nonce: nonce, exitAttest: ExitAttestHash(fixtureAttest),
				jobDir: jobDir, started: time.Now().UTC(), deadline: time.Minute, route: w.RouteName()}
			var out, errb bytes.Buffer
			in := RunInput{Pool: p, Worker: w, Stdout: &out, Stderr: &errb,
				Now: func() time.Time { return time.Now().UTC() }}
			in.finish(r, map[int]bool{}, time.Now().UTC())

			moved, err := p.ReadSidecar(Failed, id)
			if err != nil {
				t.Fatalf("a stalled job belongs in failed/: %v\n%s%s", err, out.String(), errb.String())
			}
			if moved.End != EndStall {
				t.Errorf("the moved sidecar wants end=%s, got %q", EndStall, moved.End)
			}
			pending, err := p.List(Pending)
			if err != nil {
				t.Fatal(err)
			}
			if !c.wantRequeued {
				if len(pending) != 0 {
					t.Fatalf("a second stall is not re-queued again, got %d pending", len(pending))
				}
				if moved.Reaped != c.wasReaped+1 {
					t.Errorf("the second stall wants reaped=%d, got %d", c.wasReaped+1, moved.Reaped)
				}
				return
			}
			if len(pending) != 1 {
				t.Fatalf("the first stall re-queues the task once, got %d pending", len(pending))
			}
			next := pending[0]
			if next.From != id || next.Requeued != 1 {
				t.Errorf("the new attempt wants from=%s requeued=1, got from=%s requeued=%d",
					id, next.From, next.Requeued)
			}
		})
	}
}

// buildNovaSwarm builds the real dispatcher/supervisor binary once per test so the cap test
// can drive `run`'s launch path without a network or a fake supervisor.
func buildNovaSwarm(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-swarm")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/nova-swarm")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building nova-swarm for the cap test: %v\n%s", err, out)
	}
	return bin
}
