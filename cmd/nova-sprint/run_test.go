package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestStartAndStopSayTheStateBeforeAndAfter(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	if out := ta.ok("where"); !strings.Contains(out, "SPRINT TABLE\n\nSTOPPED\n\n") || strings.Contains(out, "machine:") {
		t.Fatalf("where: %s", out)
	}
	out := ta.ok("stop")
	if !strings.Contains(out, "STOP OK before=STOPPED after=STOPPED unchanged: the machine is STOPPED already") || !strings.Contains(out, "machine: STOPPED") {
		t.Fatalf("stop when stopped: %s", out)
	}
	out = ta.ok("start")
	if !strings.Contains(out, "START OK before=STOPPED after=RUNNING changed") || !strings.Contains(out, "nothing is ticking: run: nova-sprint run") ||
		!strings.Contains(out, "0/3 0.0% -> ETA  machine: running") {
		t.Fatalf("start: %s", out)
	}
	if out := ta.ok("start"); !strings.Contains(out, "unchanged: the machine is RUNNING already") {
		t.Fatalf("start when running: %s", out)
	}
	out = ta.ok("tick")
	if !strings.Contains(out, "MOVED deal: s1-1 ready -> working") || !strings.Contains(out, "TICK OK state=RUNNING idle=no moved=2") {
		t.Fatalf("tick: %s", out)
	}
	ta.a.sleep(6 * time.Second)
	if out := ta.ok("inbox"); !strings.Contains(out, "machine: STOPPED (no tick for 6s)") {
		t.Fatalf("inbox with no tick: %s", out)
	}
	if out := ta.ok("take --as m1 --limit 1"); !strings.Contains(out, "machine: STOPPED (no tick for 6s)") {
		t.Fatalf("a verb's line with no tick: %s", out)
	}
	ta.ok("stop")
	if out := ta.ok("tick"); !strings.Contains(out, "TICK OK state=STOPPED nothing done") {
		t.Fatalf("a tick while stopped: %s", out)
	}
}

func TestRunTicksOnlyWhileRunning(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	if st == nil {
		t.Fatalf("run: %d", code)
	}
	var out, errb bytes.Buffer
	ta.a.runLoop(context.Background(), st, 20, 3, &out, &errb)
	if strings.Contains(out.String(), "MOVED") || !strings.Contains(out.String(), "machine STOPPED") {
		t.Fatalf("run while stopped:\n%s", out.String())
	}
	ta.ok("start")
	out.Reset()
	ta.a.runLoop(context.Background(), st, 20, 3, &out, &errb)
	if !strings.Contains(out.String(), "machine RUNNING") || !strings.Contains(out.String(), "MOVED deal: s1-1 ready -> working") {
		t.Fatalf("run while running:\n%s", out.String())
	}
	if out := ta.ok("where"); !strings.Contains(out, "SPRINT TABLE\n\n0/3 0.0% -> ETA\n\n") {
		t.Fatalf("where after run: %s", out)
	}
}

// whereHead is what the where view says under its title: the lines between
// SPRINT TABLE and the first table, with the clock line before the title cut.
func whereHead(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "SPRINT TABLE\n")
	if i < 0 {
		t.Fatalf("where has no title:\n%s", out)
	}
	return out[i:]
}

func TestWhereHeaderIsStoppedOrTheProgressLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	// stopped: the title, a blank line, the word, a blank line, byte for byte
	if got := whereHead(t, ta.ok("where")); !strings.HasPrefix(got, "SPRINT TABLE\n\nSTOPPED\n\n") || strings.HasPrefix(got, "SPRINT TABLE\n\nSTOPPED\n\n\n") {
		t.Fatalf("stopped:\n%q", got)
	}
	// running with no cards
	ta.ok("start")
	if got := whereHead(t, ta.ok("where")); !strings.HasPrefix(got, "SPRINT TABLE\n\n0/0 0.0% -> ETA\n\n") {
		t.Fatalf("running with no cards:\n%q", got)
	}
	// running with cards
	ta.ok("add --stream s1 --count 3")
	ta.ok("tick")
	out := ta.ok("where")
	if got := whereHead(t, out); !strings.HasPrefix(got, "SPRINT TABLE\n\n0/3 0.0% -> ETA\n\n") || strings.Contains(out, "machine:") || strings.Contains(out, "coordinator:") {
		t.Fatalf("running with cards:\n%q", out)
	}
	// running but silent: never hidden
	ta.a.sleep(6 * time.Second)
	if got := whereHead(t, ta.ok("where")); !strings.HasPrefix(got, "SPRINT TABLE\n\nSTOPPED (no tick for 6s)\n\n") {
		t.Fatalf("running but silent:\n%q", got)
	}
}

func TestWhereHeaderStoppedIsExactlyTheView(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	out := ta.ok("where")
	i := strings.Index(out, "readers")
	if i < 0 || out[:i] != "2030-01-02 03:04:05 UTC\n\nSPRINT TABLE\n\nSTOPPED\n\n" {
		t.Fatalf("stopped view:\n%q", out)
	}
}
