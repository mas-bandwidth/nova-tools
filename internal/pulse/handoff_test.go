package pulse

// SPEC-PULSE replays 26-31: handoff ends the duty shift as a verb and takeover
// begins the next one, with the coordinator's bench ownership as a lock and a
// record. Every nova-wake and nova-bus is a fixture on PATH that records its
// argv; no test calls a provider.

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shiftBench is one coordinator shift's world: the queue holding OWNER,
// HANDOFF, pending and launched; the benches; the bus clone; the fakes.
type shiftBench struct {
	queue, roots, bus, specs, arglog string
}

func setupShift(t *testing.T) shiftBench {
	t.Helper()
	base := t.TempDir()
	s := shiftBench{
		queue:  filepath.Join(base, "queue"),
		roots:  filepath.Join(base, "swarm-root") + "," + filepath.Join(base, "swarm-root-space"),
		bus:    filepath.Join(base, "bus"),
		specs:  fakePATH(t),
		arglog: filepath.Join(base, "argv.log"),
	}
	for _, d := range []string{
		filepath.Join(s.queue, "pending"),
		filepath.Join(s.queue, "launched"),
		s.bus,
		filepath.Join(base, "swarm-root"),
		filepath.Join(base, "swarm-root-space"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.fake(t, "nova-wake", fakeSpec{Default: fakeRule{Stdout: "FRIEND Stella awake age=10 source=bus-cursor\nAWAKE OK friends=1 awake=1"}})
	s.fake(t, "nova-bus", fakeSpec{Default: fakeRule{Stdout: "BO-123456"}})
	return s
}

func (s shiftBench) fake(t *testing.T, name string, spec fakeSpec) {
	t.Helper()
	spec.Log = s.arglog
	fakeTool(t, s.specs, name, spec)
}

func (s shiftBench) write(t *testing.T, rel, body string) {
	t.Helper()
	p := filepath.Join(s.queue, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s shiftBench) read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.queue, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (s shiftBench) argv(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(s.arglog)
	if err != nil {
		return ""
	}
	return string(raw)
}

// shiftJob parks one in-flight card: the card file in launched and its job
// directory on the first bench, the way manager.jobFor finds it.
func (s shiftBench) shiftJob(t *testing.T, label string) {
	t.Helper()
	s.write(t, filepath.Join("launched", label+".md"), "RESULT: "+label+" fix the slot lock\n")
	root := strings.Split(s.roots, ",")[0]
	dir := filepath.Join(root, "s1", "jobs", label)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte("RESULT: "+label+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func shiftStamp() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) }

func shiftHost(t *testing.T) string {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		t.Fatal("the bench has no hostname to own a lock with")
	}
	return strings.TrimSpace(host)
}

func runHandoff(s shiftBench, to string) (string, string, int) {
	var out, errs bytes.Buffer
	code := Handoff(HandoffInput{
		Queue: s.queue, To: to, Bus: s.bus, Roots: s.roots, As: "Rowan",
		Max: 20, Stdout: &out, Stderr: &errs, Now: shiftStamp,
	})
	return out.String(), errs.String(), code
}

func runTakeover(s shiftBench, as string) (string, string, int) {
	var out, errs bytes.Buffer
	code := Takeover(TakeoverInput{
		Queue: s.queue, As: as, Bus: s.bus, Roots: s.roots,
		Max: 20, Stdout: &out, Stderr: &errs, Now: shiftStamp,
	})
	return out.String(), errs.String(), code
}

// handoff-writes-record-and-note: handoff ends the shift -- SHIFT END on
// stdout, the loop stopped -- writes queue/OWNER freed and queue/HANDOFF with
// the last width, in-flight by bench, pending, escalations, benches and state,
// and the fixture bus records one note to the successor carrying the record.
func TestHandoffWritesRecordAndNote(t *testing.T) {
	s := setupShift(t)
	host := shiftHost(t)
	s.write(t, "OWNER", "Rowan\t"+host+"\t4242\t2026-09-15T11:00:00Z\n")
	s.write(t, "pending/card-1.md", "RESULT: card-1 fix the slot lock\n")
	s.write(t, "pending/card-2.md", "RESULT: card-2 fix the other lock\n")
	s.shiftJob(t, "card-3")
	s.write(t, "ESCALATE", "ESCALATE 2026-09-15T11:30:00Z ABSTAIN card-9: rewrite the prompt\n")
	s.write(t, "pulse.log", "PULSE WIDTH tick=9 benches=2 stop=no harvested=3 swept=0 requeued=0 failed=0 refilled=2 launched=2 free=0 undecided=0 noted=0\n")

	out, errs, code := runHandoff(s, "Stella")
	if code != 0 {
		t.Fatalf("exit = %d (stdout=%q stderr=%q)", code, out, errs)
	}
	if !strings.Contains(out, "SHIFT END") {
		t.Errorf("the shift did not end on the record: %q", out)
	}
	if !strings.Contains(out, "HANDOFF OK to=Stella inflight=1 pending=2 escalations=1") {
		t.Errorf("stdout = %q, want HANDOFF OK to=Stella inflight=1 pending=2 escalations=1", out)
	}
	if _, err := os.Stat(filepath.Join(s.queue, "OWNER")); !os.IsNotExist(err) {
		t.Errorf("OWNER is still held after the handoff")
	}
	hand := s.read(t, "HANDOFF")
	for _, want := range []string{"Stella", "Rowan", "PULSE WIDTH tick=9", "swarm-root"} {
		if !strings.Contains(hand, want) {
			t.Errorf("HANDOFF = %q, want it to carry %q", hand, want)
		}
	}
	if got := s.read(t, "LOOP"); !strings.Contains(got, "stopped") || !strings.Contains(got, "Stella") {
		t.Errorf("LOOP = %q, want the loop stopped to Stella", got)
	}
	argv := s.argv(t)
	if !strings.Contains(argv, "nova-wake awake") {
		t.Errorf("the successor was never checked awake: %q", argv)
	}
	if !strings.Contains(argv, "nova-bus send") {
		t.Errorf("no bus note went to the successor: %q", argv)
	}
}

// handoff-refuses-asleep-successor: the successor asleep by nova-wake awake --
// a non-zero exit or no awake FRIEND line -- is HANDOFF REFUSED naming the
// remedy, exit 2, no record, no note, OWNER unchanged.
func TestHandoffRefusesAsleepSuccessor(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec fakeSpec
	}{
		{"non-zero exit", fakeSpec{Default: fakeRule{Stdout: "AWAKE REFUSED Stella is asleep", Exit: 1}}},
		{"asleep line", fakeSpec{Default: fakeRule{Stdout: "FRIEND Stella asleep age=900 source=bus-cursor\nAWAKE OK friends=1 awake=0"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setupShift(t)
			s.fake(t, "nova-wake", tc.spec)
			owner := "Rowan\t" + shiftHost(t) + "\t4242\t2026-09-15T11:00:00Z\n"
			s.write(t, "OWNER", owner)
			s.write(t, "pending/card-1.md", "RESULT: card-1 fix the slot lock\n")

			out, errs, code := runHandoff(s, "Stella")
			if code != 2 {
				t.Fatalf("exit = %d (stdout=%q stderr=%q), want 2", code, out, errs)
			}
			if !strings.Contains(errs, "HANDOFF REFUSED") || !strings.Contains(errs, "(") {
				t.Errorf("stderr = %q, want HANDOFF REFUSED with a remedy", errs)
			}
			if _, err := os.Stat(filepath.Join(s.queue, "HANDOFF")); !os.IsNotExist(err) {
				t.Errorf("a refused handoff wrote a HANDOFF record")
			}
			if strings.Contains(s.argv(t), "nova-bus send") {
				t.Errorf("a refused handoff noted the successor: %q", s.argv(t))
			}
			if got := s.read(t, "OWNER"); got != owner {
				t.Errorf("OWNER = %q, want it unchanged %q", got, owner)
			}
		})
	}
}

// handoff-finishes-harvest-first: a harvest in progress is finished before the
// handoff proceeds; a handoff over a mid-harvest lock is refused with the
// remedy, the lock and the OWNER untouched.
func TestHandoffFinishesHarvestFirst(t *testing.T) {
	s := setupShift(t)
	owner := "Rowan\t" + shiftHost(t) + "\t4242\t2026-09-15T11:00:00Z\n"
	s.write(t, "OWNER", owner)
	s.write(t, "HARVEST.LOCK", "harvest pulse-7 in progress\n")

	out, errs, code := runHandoff(s, "Stella")
	if code != 2 {
		t.Fatalf("exit = %d (stdout=%q stderr=%q), want 2", code, out, errs)
	}
	if !strings.Contains(errs, "HANDOFF REFUSED") || !strings.Contains(errs, "harvest") {
		t.Errorf("stderr = %q, want HANDOFF REFUSED naming the harvest and its remedy", errs)
	}
	if _, err := os.Stat(filepath.Join(s.queue, "HARVEST.LOCK")); err != nil {
		t.Errorf("the harvest lock is gone: the harvest was not finished first")
	}
	if got := s.read(t, "OWNER"); got != owner {
		t.Errorf("OWNER = %q, want it unchanged %q", got, owner)
	}
	if _, err := os.Stat(filepath.Join(s.queue, "HANDOFF")); !os.IsNotExist(err) {
		t.Errorf("a mid-harvest handoff wrote a HANDOFF record")
	}
}

// takeover-refuses-live-owner: OWNER names a live process on a reachable host
// -- TAKEOVER REFUSED owner=<name> pid=<n> host=<h>, exit 2, nothing taken.
func TestTakeoverRefusesLiveOwner(t *testing.T) {
	s := setupShift(t)
	host := shiftHost(t)
	live := "Rowan\t" + host + "\t" + strconv.Itoa(os.Getpid()) + "\t2026-09-15T11:00:00Z\n"
	s.write(t, "OWNER", live)

	out, errs, code := runTakeover(s, "Stella")
	if code != 2 {
		t.Fatalf("exit = %d (stdout=%q stderr=%q), want 2", code, out, errs)
	}
	want := "TAKEOVER REFUSED owner=Rowan pid=" + strconv.Itoa(os.Getpid()) + " host=" + host
	if !strings.Contains(errs, want) {
		t.Errorf("stderr = %q, want %q", errs, want)
	}
	if got := s.read(t, "OWNER"); got != live {
		t.Errorf("OWNER = %q, want the live lock untouched %q", got, live)
	}
}

// takeover-takes-stale-lock-with-note: OWNER names a dead process or an
// unreachable host -- the lock is taken, one NOTE line says so, and the loop
// and a shift start on the same queue.
func TestTakeoverTakesStaleLockWithNote(t *testing.T) {
	host := shiftHost(t)
	for _, tc := range []struct {
		name  string
		owner string
	}{
		{"dead process", "Rowan\t" + host + "\t2147483647\t2026-09-15T11:00:00Z\n"},
		{"unreachable host", "Rowan\tbench-that-never-was\t4242\t2026-09-15T11:00:00Z\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setupShift(t)
			s.write(t, "OWNER", tc.owner)
			out, errs, code := runTakeover(s, "Stella")
			if code != 0 {
				t.Fatalf("exit = %d (stdout=%q stderr=%q), want 0", code, out, errs)
			}
			if strings.Count(out, "NOTE") != 1 {
				t.Errorf("stdout = %q, want exactly one NOTE line for the stale lock", out)
			}
			if !strings.Contains(out, "TAKEOVER OK") {
				t.Errorf("stdout = %q, want TAKEOVER OK after the stale lock", out)
			}
			got := s.read(t, "OWNER")
			if !strings.HasPrefix(got, "Stella\t"+host+"\t") {
				t.Errorf("OWNER = %q, want Stella holding it on this bench", got)
			}
			if loop := s.read(t, "LOOP"); !strings.Contains(loop, "running") || !strings.Contains(loop, "Stella") {
				t.Errorf("LOOP = %q, want the loop running under Stella", loop)
			}
		})
	}
}

// takeover-inherits-queue: the taken HANDOFF record's inflight, pending and
// escalations are inherited and printed as TAKEOVER OK from=<name>
// inherited=<inflight/pending/escalations>.
func TestTakeoverInheritsQueue(t *testing.T) {
	s := setupShift(t)
	s.write(t, "OWNER", "Rowan\t"+shiftHost(t)+"\t4242\t2026-09-15T11:00:00Z\n")
	s.write(t, "pending/card-1.md", "RESULT: card-1 fix the slot lock\n")
	s.write(t, "pending/card-2.md", "RESULT: card-2 fix the other lock\n")
	s.shiftJob(t, "card-3")
	s.write(t, "ESCALATE", "ESCALATE 2026-09-15T11:30:00Z ABSTAIN card-9: rewrite the prompt\n")
	s.write(t, "pulse.log", "PULSE WIDTH tick=9 benches=2 stop=no harvested=3 swept=0 requeued=0 failed=0 refilled=2 launched=2 free=0 undecided=0 noted=0\n")

	if out, errs, code := runHandoff(s, "Stella"); code != 0 {
		t.Fatalf("handoff exit = %d (stdout=%q stderr=%q)", code, out, errs)
	}
	out, errs, code := runTakeover(s, "Stella")
	if code != 0 {
		t.Fatalf("exit = %d (stdout=%q stderr=%q), want 0", code, out, errs)
	}
	if !strings.Contains(out, "TAKEOVER OK from=Rowan inherited=1/2/1") {
		t.Errorf("stdout = %q, want TAKEOVER OK from=Rowan inherited=1/2/1", out)
	}
}

// handoff-moves-work-ownership: when nova-work is open, handoff also moves
// the coordinator ownership record in the tree -- the generation bumps, a
// fresh token is drawn, and the :handoff event SPEC-WORK names is appended.
func TestHandoffMovesWorkOwnership(t *testing.T) {
	s := setupShift(t)
	s.write(t, "OWNER", "Rowan\t"+shiftHost(t)+"\t4242\t2026-09-15T11:00:00Z\n")
	work := filepath.Join(s.queue, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "OWNER"), []byte("name=Rowan generation=3 token=old fencing=2026-09-15T11:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	code := Handoff(HandoffInput{
		Queue: s.queue, To: "Stella", Bus: s.bus, Roots: s.roots, As: "Rowan",
		Work: work, Max: 20, Stdout: &out, Stderr: &errs, Now: shiftStamp,
	})
	if code != 0 {
		t.Fatalf("exit = %d (stdout=%q stderr=%q)", code, out.String(), errs.String())
	}
	owner, err := os.ReadFile(filepath.Join(work, "OWNER"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(owner), "name=Stella") || !strings.Contains(string(owner), "generation=4") {
		t.Errorf("work OWNER = %q, want Stella at generation 4", owner)
	}
	if strings.Contains(string(owner), "token=old") {
		t.Errorf("work OWNER = %q, want a fresh token, not the old one", owner)
	}
	events, err := os.ReadFile(filepath.Join(work, "EVENTS"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), ":handoff") || !strings.Contains(string(events), "Stella") {
		t.Errorf("work EVENTS = %q, want the :handoff event to Stella", events)
	}
}
