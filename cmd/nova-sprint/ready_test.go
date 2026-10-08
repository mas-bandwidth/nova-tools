package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H2: two different readers' ok of a primary the pump holds (its CI red at
// its head, the ci red judgment acknowledged) is a judgment, ready to accept,
// written by the read that completes the pair; it stays in the inbox whatever
// the cursor; accept over its group with --group and --expect closes it. A
// primary nothing holds is the tick's to accept, no judgment
// (TestTheTickAcceptsAndReadOkSaysNothingWaits).
func TestReadyToAcceptIsAJudgmentAcceptedByGroup(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3 --brief-file " + proBriefFile(t))
	ta.deal(3)
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1")
	ta.ok("ask")
	ta.ok("ci s1-1 s1-2 s1-3 --red --run 1")
	red := ta.group(sprint.NCIRed, "s1")
	ta.ok("ack " + strings.Join(red.Notes, ",") + " --reason 'a flaky runner'")
	// the first reads round the readers: s1-1 reader-a, s1-2 reader-b, s1-3 reader-a; each ok,
	// the second is asked of the other
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
	ta.ok("read --as reader-b --ok s1-2.r1.reader-b")
	ta.ok("read --as reader-a --ok s1-3.r1.reader-a")
	ta.ok("ask")
	ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
	ta.ok("read --as reader-a --ok s1-2.r1.reader-a")
	// s1-3: one reader ok, its second read outstanding: not ready
	ta.ok("inbox --read")
	g := ta.group(sprint.NReadyToAccept, "s1")
	require.Equal(t, sprint.Judgment, g.Kind, "the ready group after the cursor moved: %+v", g)
	require.Equal(t, 2, g.Size, "the ready group after the cursor moved: %+v", g)
	require.Equal(t, []string{"s1-1", "s1-2"}, g.Primaries, "the ready group after the cursor moved: %+v", g)
	var line string
	for _, c := range g.Commands {
		if c.Decision == "accept" {
			line = c.Lines[0]
		}
	}
	want := "nova-sprint accept --group " + g.ID + " --expect 2 --answers " + strings.Join(g.Notes, ",")
	require.Equal(t, want, line, "the accept command (%+v)", g.Commands)
	for _, d := range []string{"rework", "drop"} {
		found := false
		for _, c := range g.Commands {
			found = found || c.Decision == d
		}
		require.True(t, found, "no %s decision: %+v", d, g.Commands)
	}
	out := ta.ok(strings.TrimPrefix(line, "nova-sprint "))
	require.Contains(t, out, "GROUP "+g.ID+" acted on 2, the group had 2 when printed", "accept by group")
	require.Contains(t, out, "s1-1 review -> merging", "accept by group")
	require.Contains(t, out, "s1-2 review -> merging", "accept by group")
	for _, x := range ta.inboxGroups() {
		require.False(t, x.Type == sprint.NReadyToAccept && x.Kind == sprint.Judgment, "accept left the judgment open: %+v", x)
	}
	ta.clean()
}

// The tick accepts a primary whose reads are all ok: no ready to accept
// judgment opens, a STOPPED machine's inbox counts it among the moves due, the
// first tick after start moves it to merging and tells the seat (ready to
// merge), and accept --read-ok, the verb for a stuck case, then says nothing
// waits.
func TestTheTickAcceptsAndReadOkSaysNothingWaits(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	for _, g := range ta.inboxGroups() {
		require.False(t, g.Type == sprint.NReadyToAccept, "a ready to accept judgment: %+v", g)
	}
	out := ta.ok("inbox")
	require.Contains(t, out, "the machine is STOPPED and 2 moves are due", "the accepts are moves due:\n%s", out)
	ta.ok("start")
	ta.ok("tick")
	for _, id := range []string{"s1-1", "s1-2"} {
		require.Equal(t, sprint.Merging, ta.primary(id).Col, "%s accepted by the tick", id)
	}
	g := ta.group(sprint.NReadyToMerge, "s1")
	require.Equal(t, []string{"s1-1", "s1-2"}, g.Primaries, "the seat is told: %+v", g)
	require.Empty(t, g.Commands, "a notice: nothing to answer: %+v", g)
	out = ta.ok("accept --read-ok")
	require.Contains(t, out, "nothing waits: the tick accepts", "accept --read-ok with nothing waiting:\n%s", out)
	ta.clean()
}
