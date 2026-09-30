package main

import (
	"strings"
	"testing"
	"time"
)

// inbox --wait (errata 3 amendment 8): with no tick end to come it waits out
// its --timeout on the store's clock, says so, and shows the inbox; a
// timeout of zero is refused.
func TestInboxWaitRunsOutItsTimeout(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.a.now()
	out := ta.ok("inbox --wait --timeout 2s")
	if !strings.Contains(out, "inbox --wait: no tick end in 2s") {
		t.Fatalf("inbox --wait with nothing to come:\n%s", out)
	}
	if waited := ta.a.now().Sub(before); waited < 2*time.Second { // wall-ok: the test app's fake clock, which the wait advances
		t.Fatalf("inbox --wait returned after %s of the store's clock, before its timeout of 2s", waited)
	}
	if code, _, errs := ta.do("inbox --wait --timeout 0s"); code == 0 || !strings.Contains(errs, "--timeout above zero") {
		t.Fatalf("a timeout of zero: exit %d\n%s", code, errs)
	}
}

// accept --read-ok takes only a primary with ok reads from two different
// readers: with one ok read it accepts nothing and the primary stays in
// review; named, it is refused, saying what it lacks.
func TestAcceptReadOkRefusesOneOkRead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 5")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 10")
	if out := ta.ok("accept --read-ok"); !strings.Contains(out, "moved=0") {
		t.Fatalf("accept --read-ok with one ok read:\n%s", out)
	}
	ta.ok("tick") // the pump applies the queued finish; one ok read accepts nothing
	if out := ta.ok("card s1-1"); !strings.Contains(out, "stream s1   review") {
		t.Fatalf("s1-1 after accept --read-ok with one ok read:\n%s", out)
	}
	if code, out, errs := ta.do("accept s1-1"); code == 0 || !strings.Contains(out+errs, "needs ok from two different readers") {
		t.Fatalf("accept s1-1 with one ok read: exit %d\n%s%s", code, out, errs)
	}
}
