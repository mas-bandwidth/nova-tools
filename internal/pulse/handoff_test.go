package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Handoff and Takeover verbs (SPEC-PULSE.md, "Handoff", replay set
// 26-31): the manager's workday ends by handoff and begins again by takeover,
// OWNER is the lock, HANDOFF is the record, and one bus note carries the
// record to the successor. Every child (nova-wake, nova-bus, gh, git) is a
// fixture on PATH that records its argv.

type handoffBench struct {
	queue, bus, specs, arglog string
}

// setupHandoff builds a queue dir, a fake bus clone, and puts the fakes on PATH.
func setupHandoff(t *testing.T) handoffBench {
	t.Helper()
	base := t.TempDir()
	b := handoffBench{
		queue:  filepath.Join(base, "queue"),
		bus:    filepath.Join(base, "bus"),
		specs:  fakePATH(t),
		arglog: filepath.Join(base, "argv.log"),
	}
	for _, d := range []string{b.queue, b.bus} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func (b handoffBench) teach(t *testing.T, name string, s fakeSpec) {
	t.Helper()
	s.Log = b.arglog
	fakeTool(t, b.specs, name, s)
}

func (b handoffBench) write(t *testing.T, rel, body string) string {
	t.Helper()
	p := filepath.Join(b.queue, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (b handoffBench) read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.queue, rel))
	if err != nil {
		return ""
	}
	return string(raw)
}

func (b handoffBench) argv(t *testing.T) string {
	t.Helper()
	raw, _ := os.ReadFile(b.arglog)
	return string(raw)
}

func (b handoffBench) runHandoff(t *testing.T, to, from, width string) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	stamp := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	code := Handoff(HandoffInput{
		Queue: b.queue, Bus: b.bus, To: to, From: from, Width: width,
		Stdout: &out, Stderr: &errs, Now: func() time.Time { return stamp },
	})
	return out.String(), errs.String(), code
}

func (b handoffBench) runTakeover(t *testing.T, as string) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	stamp := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	code := Takeover(TakeoverInput{
		Queue: b.queue, Bus: b.bus, As: as,
		Stdout: &out, Stderr: &errs, Now: func() time.Time { return stamp },
	})
	return out.String(), errs.String(), code
}

// handoff-writes-record-and-note (replay 26): handoff --to <name> ends the
// shift, writes OWNER freed and HANDOFF carrying the last width, in-flight by
// bench, pending, escalations and benches, posts one bus note to the successor
// carrying the record, and prints the SHIFT END line followed by HANDOFF OK
// to=<name> inflight=<n> pending=<n> escalations=<n>.
func TestHandoffWritesRecordAndNote(t *testing.T) {
	b := setupHandoff(t)
	b.teach(t, "nova-wake", fakeSpec{Default: fakeRule{Stdout: "FRIEND Stella awake age=10 source=bus-cursor\nAWAKE OK friends=1 awake=1\n"}})
	b.teach(t, "nova-bus", fakeSpec{Rules: []fakeRule{
		{Arg: 2, Equals: "send", Stdout: "SEND OK id=bo-aaaaaaaaaaaa"},
	}})
	b.write(t, "PENDING", "card-1\ncard-2\n")
	b.write(t, "ESCALATE", "ESCALATE 2026-09-15T11:00:00Z ABSTAIN card-1: reason=x\n")
	hname, _ := os.Hostname()
	b.write(t, "OWNER", "Rowan\t"+hname+"\t"+strconv.Itoa(os.Getpid())+"\t2026-09-15T11:00:00Z\n")
	out, errs, code := b.runHandoff(t, "Stella", "Rowan", "PULSE WIDTH in-flight=3 free=2 pool=0 queued=0")

	if code != 0 {
		t.Fatalf("handoff exit = %d, want 0 (stdout=%q stderr=%q)", code, out, errs)
	}
	if !strings.Contains(out, "SHIFT END") {
		t.Errorf("handoff did not write the SHIFT END line: %q", out)
	}
	if !strings.Contains(out, "HANDOFF OK to=Stella inflight=") {
		t.Errorf("handoff did not print HANDOFF OK; stdout=%q", out)
	}
	if !strings.Contains(out, "pending=") || !strings.Contains(out, "escalations=") {
		t.Errorf("HANDOFF OK is missing pending= or escalations=: %q", out)
	}
	record := b.read(t, "HANDOFF")
	if !strings.Contains(record, "Stella") || !strings.Contains(record, "Rowan") {
		t.Errorf("HANDOFF record missing successor or current owner:\n%s", record)
	}
	if !strings.Contains(record, "in-flight") || !strings.Contains(record, "pending") || !strings.Contains(record, "escalations") {
		t.Errorf("HANDOFF record missing the spec fields:\n%s", record)
	}
	if own := b.read(t, "OWNER"); strings.Contains(own, "Rowan") {
		t.Errorf("OWNER was not released after a successful handoff:\n%s", own)
	}
	argv := b.argv(t)
	if !strings.Contains(argv, "nova-wake awake --bus ") {
		t.Errorf("handoff did not probe liveness: argv=%q", argv)
	}
	if !strings.Contains(argv, "nova-bus send") {
		t.Errorf("handoff did not post a bus note to the successor: argv=%q", argv)
	}
}

// handoff-refuses-asleep-successor (replay 27): a successor a fixture nova-wake
// awake says is asleep is a refusal, with the remedy in parentheses, exit 2,
// and no record, no note, OWNER unchanged.
func TestHandoffRefusesAsleepSuccessor(t *testing.T) {
	b := setupHandoff(t)
	b.teach(t, "nova-wake", fakeSpec{Default: fakeRule{Stdout: "FRIEND Stella asleep age=600 source=bus-cursor\nAWAKE OK friends=1 asleep=1\n"}})
	b.teach(t, "nova-bus", fakeSpec{})
	hname, _ := os.Hostname()
	b.write(t, "OWNER", "Rowan\t"+hname+"\t"+strconv.Itoa(os.Getpid())+"\t2026-09-15T11:00:00Z\n")

	out, errs, code := b.runHandoff(t, "Stella", "Rowan", "PULSE WIDTH in-flight=1 free=4 pool=0 queued=0")
	if code != 2 {
		t.Fatalf("handoff to an asleep successor exit = %d, want 2 (stdout=%q stderr=%q)", code, out, errs)
	}
	if !strings.Contains(errs, "HANDOFF REFUSED") {
		t.Errorf("the refusal line is not on stderr: %q", errs)
	}
	if !strings.HasSuffix(strings.TrimSpace(errs), ")") {
		t.Errorf("the refusal did not name the remedy in parentheses: %q", errs)
	}
	if record := b.read(t, "HANDOFF"); record != "" {
		t.Errorf("HANDOFF was written although the handoff was refused:\n%s", record)
	}
	if own := b.read(t, "OWNER"); !strings.Contains(own, "Rowan") {
		t.Errorf("OWNER was released although the handoff was refused:\n%s", own)
	}
	if argv := b.argv(t); strings.Contains(argv, "nova-bus send") {
		t.Errorf("a refused handoff still sent a bus note: %q", argv)
	}
}

// handoff-finishes-harvest-first (replay 28): a harvest in progress is the
// refusal that tells the caller to wait, named on its own line, and the SHIFT
// END line is not printed.
func TestHandoffRefusesMidHarvest(t *testing.T) {
	b := setupHandoff(t)
	b.teach(t, "nova-wake", fakeSpec{})
	b.teach(t, "nova-bus", fakeSpec{})
	b.write(t, "HARVEST", "1\n")
	hname, _ := os.Hostname()
	b.write(t, "OWNER", "Rowan\t"+hname+"\t"+strconv.Itoa(os.Getpid())+"\t2026-09-15T11:00:00Z\n")

	out, errs, code := b.runHandoff(t, "Stella", "Rowan", "PULSE WIDTH in-flight=0 free=4 pool=0 queued=0")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "HANDOFF REFUSED") || !strings.Contains(errs, "harvest") {
		t.Errorf("mid-harvest refusal did not name the remedy: %q", errs)
	}
	if strings.Contains(out, "SHIFT END") {
		t.Errorf("a mid-harvest handoff still printed SHIFT END: %q", out)
	}
	if record := b.read(t, "HANDOFF"); record != "" {
		t.Errorf("HANDOFF was written mid-harvest:\n%s", record)
	}
}

// takeover-refuses-live-owner (replay 29): OWNER names a live process on a
// reachable host -- the takeover is refused with TAKEOVER REFUSED owner=<name>
// pid=<n> host=<h>, exit 2, and nothing is taken.
func TestTakeoverRefusesLiveOwner(t *testing.T) {
	b := setupHandoff(t)
	b.teach(t, "nova-wake", fakeSpec{})
	b.teach(t, "nova-bus", fakeSpec{})
	hname, _ := os.Hostname()
	b.write(t, "OWNER", "Rowan\t"+hname+"\t"+strconv.Itoa(os.Getpid())+"\t2026-09-15T11:00:00Z\n")

	out, errs, code := b.runTakeover(t, "Stella")
	if code != 2 {
		t.Fatalf("takeover with a live owner exit = %d, want 2 (stdout=%q stderr=%q)", code, out, errs)
	}
	if !strings.Contains(errs, "TAKEOVER REFUSED") {
		t.Errorf("the refusal line is not on stderr: %q", errs)
	}
	for _, want := range []string{"owner=Rowan", "pid=" + strconv.Itoa(os.Getpid()), "host=" + hname} {
		if !strings.Contains(errs, want) {
			t.Errorf("refusal is missing %q: %q", want, errs)
		}
	}
	if own := b.read(t, "OWNER"); !strings.Contains(own, "Rowan") {
		t.Errorf("a refused takeover rewrote OWNER:\n%s", own)
	}
}

// takeover-takes-stale-lock-with-note (replay 30): a stale lock (the recorded
// pid is not alive, or the host is not the local one) is taken with one NOTE
// line and the new owner is recorded.
func TestTakeoverTakesStaleLockWithNote(t *testing.T) {
	b := setupHandoff(t)
	b.teach(t, "nova-wake", fakeSpec{})
	b.teach(t, "nova-bus", fakeSpec{})
	b.write(t, "OWNER", "Rowan\tsome-other-host\t"+strconv.Itoa(os.Getpid())+"\t2026-09-15T11:00:00Z\n")

	out, errs, code := b.runTakeover(t, "Stella")
	if code != 0 {
		t.Fatalf("takeover with a stale lock exit = %d, want 0 (stdout=%q stderr=%q)", code, out, errs)
	}
	if !strings.Contains(out, "TAKEOVER OK") {
		t.Errorf("takeover did not print TAKEOVER OK: %q", out)
	}
	if !strings.Contains(out, "from=Rowan") {
		t.Errorf("takeover did not name the previous owner: %q", out)
	}
	if !strings.Contains(out, "NOTE") {
		t.Errorf("a taken stale lock did not print a NOTE line: %q", out)
	}
	if own := b.read(t, "OWNER"); !strings.Contains(own, "Stella") {
		t.Errorf("OWNER was not rewritten with the new name:\n%s", own)
	}
}

// takeover-inherits-queue (replay 31): the taken HANDOFF record's inflight,
// pending and escalations are inherited and printed as TAKEOVER OK from=<name>
// inherited=<inflight>/<pending>/<escalations>.
func TestTakeoverInheritsQueue(t *testing.T) {
	b := setupHandoff(t)
	b.teach(t, "nova-wake", fakeSpec{})
	b.teach(t, "nova-bus", fakeSpec{})
	b.write(t, "OWNER", "Rowan\tsome-other-host\t999999\t2026-09-15T11:00:00Z\n")
	b.write(t, "HANDOFF", "Stella\tRowan\tPULSE WIDTH in-flight=7 free=2 pool=0 queued=3\tin-flight=7\tpending=12\tescalations=4\tbenches=2\tstate=ended\n")

	out, _, code := b.runTakeover(t, "Stella")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	wantSubs := []string{"from=Rowan", "inherited=7/12/4"}
	for _, w := range wantSubs {
		if !strings.Contains(out, w) {
			t.Errorf("TAKEOVER OK is missing %q: %q", w, out)
		}
	}
	if !strings.Contains(out, "TAKEOVER OK") {
		t.Errorf("TAKEOVER OK was not printed: %q", out)
	}
}
