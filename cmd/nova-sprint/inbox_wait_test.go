package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	require.Contains(t, out, "inbox --wait: nothing new in 2s", "inbox --wait with nothing to come")
	require.GreaterOrEqual(t, ta.a.now().Sub(before), 2*time.Second, "inbox --wait returned before its timeout of 2s of the store's clock") // wall-ok: the test app's fake clock, which the wait advances
	code, _, errs := ta.do("inbox --wait --timeout 0s")
	require.NotEqual(t, 0, code, "a timeout of zero: exit %d\n%s", code, errs)
	require.Contains(t, errs, "--timeout above zero", "a timeout of zero: exit %d\n%s", code, errs)
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
	require.Contains(t, ta.ok("accept --read-ok"), "moved=0", "accept --read-ok with one ok read")
	ta.ok("tick") // the pump applies the queued finish; one ok read accepts nothing
	require.Contains(t, ta.ok("card s1-1"), "stream s1   review", "s1-1 after accept --read-ok with one ok read")
	code, out, errs := ta.do("accept s1-1")
	require.NotEqual(t, 0, code, "accept s1-1 with one ok read: exit %d\n%s%s", code, out, errs)
	require.Contains(t, out+errs, "needs ok from two different readers", "accept s1-1 with one ok read: exit %d\n%s%s", code, out, errs)
}
