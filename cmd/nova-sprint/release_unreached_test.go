package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// release of a sentinel not yet reached (nova-tools#5096 item c13: "ready went to 0,
// working fell from 60 to 27 while 116 cards waited behind diary-inflight and
// docs-inflight"): it is refused while anything it waits for has not started, with one
// line naming the first such wait and its state (waiting, ready, or working with its
// work card dealt and not taken), and accepted once each wait has landed, been dropped,
// or is in flight (working and taken, in review, merging). The cards behind it and the
// card of another stream that names it go to ready in the same step, what it still
// waited for is waived on it with the coordinator's name, and its reason is recorded.
func TestReleaseOfAnUnreachedSentinelWhoseWaitsAreInFlight(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream s1 --count 4 --actor lead --brief-file " + proBriefFile(t))
	ta.ok("add --stream s4 d --held --actor lead --brief-file " + proBriefFile(t))
	ta.ok("add --stream s1 --sentinel stop --needs d --actor lead")
	ta.ok("add --stream s1 b --actor lead --brief-file " + proBriefFile(t))
	ta.ok("add --stream s2 c --needs stop --actor lead --brief-file " + proBriefFile(t))
	ta.ok("add --stream s3 --sentinel gate --needs b --actor lead")
	ta.deal(3)
	ta.ok("take --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	ta.ok("ask --actor lead")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
	ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
	ta.ok("accept s1-1 --actor lead")
	require.Equal(t, sprint.Merging, ta.primary("s1-1").Col)
	require.Equal(t, sprint.Review, ta.primary("s1-2").Col)
	require.Equal(t, sprint.Working, ta.primary("s1-3").Col)

	refused := func(line, want string) {
		t.Helper()
		code, out, errs := ta.do(line)
		require.Equal(t, 1, code, "%s: %s%s", line, out, errs)
		require.Contains(t, errs, want, "%s: %s%s", line, out, errs)
	}
	why := "; release lands a sentinel whose waits have each landed, been dropped, or are in flight (taken, in review or merging)"
	refused("release stop --reason 'the fleet starves' --actor lead", "not reached: it waits for d (waiting)"+why)
	ta.ok("drop d --reason 'not wanted' --actor lead")
	refused("release stop --reason 'the fleet starves' --actor lead", "not reached: it waits for s1-4 (ready)"+why)
	ta.deal(1)
	refused("release stop --reason 'the fleet starves' --actor lead", "not reached: it waits for s1-4 (working, its work card not taken)"+why)
	refused("release gate --reason 'the fleet starves' --actor lead", "not reached: it waits for b (waiting)"+why)
	require.Equal(t, sprint.Waiting, ta.primary("stop").Col, "a refused release moved the sentinel")
	require.Equal(t, sprint.Waiting, ta.primary("b").Col, "a refused release moved what waits behind it")

	ta.ok("drop s1-4 --reason 'done elsewhere' --actor lead") // a dropped card of the stream is off its line
	out := ta.ok("release stop --reason 'the fleet starves; what is before it is in flight' --actor lead")
	require.Contains(t, out, "sentinel stop waiting -> landed (released by lead before it was reached, past d (dropped), s1-1 (merging), s1-2 (review), s1-3 (working, taken)); 2 cards are now ready", "release")
	stop := ta.primary("stop")
	require.Equal(t, sprint.Landed, stop.Col, "stop: %v", stop.Fields)
	require.Empty(t, stop.F("reached"), "stop: %v", stop.Fields)
	require.Equal(t, "the fleet starves; what is before it is in flight", stop.F("release_reason"), "stop: %v", stop.Fields)
	require.Equal(t, "lead", stop.F("released_by"), "stop: %v", stop.Fields)
	require.Equal(t, "d,s1-1,s1-2,s1-3", stop.F("waived"), "stop: %v", stop.Fields)
	require.Equal(t, "lead", stop.F("waived_by"), "stop: %v", stop.Fields)
	require.Equal(t, sprint.Ready, ta.primary("b").Col, "behind the sentinel")
	require.Equal(t, sprint.Ready, ta.primary("c").Col, "names the sentinel from another stream")
	require.Equal(t, sprint.Waiting, ta.primary("gate").Col, "gate still waits for b, which has not started")
	ta.clean()

	// the work in flight lands after the stop as it would have: nothing is owed by it
	ta.ok("merge --stream s1")
	require.Equal(t, sprint.Landed, ta.primary("s1-1").Col)
	ta.clean()
}
