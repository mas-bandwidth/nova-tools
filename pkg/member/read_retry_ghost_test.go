package member

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ghost reads of 2026-10-11 (v1.2.14 BLOCKER, a-member-hands-back-a-read-it-is-not-
// running; the port of nova-sprint #112): b2's log said "read schema-c-t04-versions.r8.b2:
// the reader's environment ended it (the wall refused a write outside the job); no verdict
// recorded; run again in 15s" at 21:55Z and nothing more until "reaped ... no longer in the
// queue" at 22:50Z, when the server's 60-minute read deadline took it back. A member's queue
// sends no packet for a card it names in --have (queueArgs), so the retry waited for a packet
// that never came: the launch stayed in the running map with its result and no child, the
// member named it in its beat, the server left it working, and it held half a lane. The
// retry runs from the launch's own packet.
//
// The queue here answers as the server does: the read, working on this member's row, with no
// packet once the member names it in --have.
func TestAReadWhoseStageFailedIsRunAgainNotHeldAsAGhost(t *testing.T) {
	t.Parallel()
	g := newWidthRig(2, Config{As: "m"})
	p := Packet{Card: "p1.r1.m", Kind: "read", As: "m", Primary: "p1", Attempt: 1, Gen: 1, Epoch: 7, Head: fullSha}
	g.s.set("queue", 0, queueJSON(t, 7, ready(p.Card)))
	g.s.set("take", 0, takeJSON(t, p))
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	require.Equal(t, []string{p.Card}, g.r.started())

	// from here the queue lists the read working on this row and, as the member names it in
	// --have, sends no packet for it
	g.s.set("queue", 0, queueJSON(t, 7, queueCard{ID: p.Card, Table: "fleet", Row: "m", Col: "working", Gen: 1, Attempt: 1}))
	g.s.set("take", 0, takeJSON(t))
	g.r.child(p.Card).end(Result{End: EndEnvironment, Report: "the wall refused a write outside the job"})
	_, err = g.tickAt(t, 1)
	require.NoError(t, err)
	require.Contains(t, g.out.String(), "read "+p.Card+": the reader's environment ended it (the wall refused a write outside the job); no verdict recorded; run again in 15s")
	queues := g.s.lines("queue")
	require.NotEmpty(t, queues)
	require.Contains(t, queues[len(queues)-1], "--have "+p.Card, "the member names the read it holds, so the queue sends it no packet")

	retry := int64(ReadStageRetry/time.Second) + 2
	_, err = g.tickAt(t, retry)
	require.NoError(t, err)
	require.Equal(t, []string{p.Card, p.Card}, g.r.started(), "the read is run again from the launch's own packet: the queue sends none for a card in --have")
	assert.Equal(t, 1, g.m.Running(), "the member holds the read for a running child, never a held result with no child")
	assert.False(t, g.r.child(p.Card).Done(), "the read the member names runs")

	// a pass later still the member holds what runs, never a ghost; the retry is once
	_, err = g.tickAt(t, retry+60)
	require.NoError(t, err)
	assert.Equal(t, []string{p.Card, p.Card}, g.r.started(), "run again once, not every pass")
	assert.Empty(t, g.s.lines("return"), "a retry that runs hands nothing back")

	// a second failure falls to the return, as for a reader: the read leaves this member at
	// once, never held to the deadline
	g.s.reset()
	g.r.child(p.Card).end(Result{End: EndStaging, Staging: "git checkout failed", Report: "no child ran"})
	_, err = g.tickAt(t, retry+61)
	require.NoError(t, err)
	got := g.s.lines("return")
	require.Len(t, got, 1, "the second stage failure is handed back")
	assert.True(t, strings.HasPrefix(got[0], "read --as m --return "+p.Card+" --reason "), got[0])
	assert.Zero(t, g.m.Running(), "nothing held")
}
