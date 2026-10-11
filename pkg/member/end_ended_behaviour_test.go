package member

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// endRig is a member over the package's script sprint and runner with the pusher given,
// its output kept, and no card taken yet: a test names the queue it wants.
func endRig(pu Pusher, cfg Config) *rig {
	s, rn, out := newScript(), newRunner(), &bytes.Buffer{}
	return &rig{m: New(cfg, s, rn, pu, out), s: s, r: rn, out: out}
}

// startWork recovers one work card at gen 1 and returns its ended child, ready for the
// test to end with the result it names.
func startWork(t *testing.T, g *rig, p *Packet) {
	t.Helper()
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.NotNil(t, g.r.child("c1"), "the recovery starts the card: %s", g.out.String())
}

// A member with no pusher whose work child ended with a head pushes nothing and says
// why in the finish (member.go, endEnded's pusher switch).
func TestAWorkFinishWithNoPusherSaysSo(t *testing.T) {
	t.Parallel()
	g := endRig(nil, Config{As: "m", Width: 2})
	p := pk("c1")
	startWork(t, g, &p)
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.reset()
	_, err := g.tick(t)
	require.NoError(t, err)
	fin := g.s.lines("finish")
	require.Len(t, fin, 1, g.out.String())
	assert.Contains(t, fin[0], "this member has no pusher")
	assert.NotContains(t, fin[0], "pushed=")
}

// A packet naming no branch is not pushed and says so in the finish (member.go,
// endEnded's pusher switch).
func TestAWorkFinishWithNoBranchIsNotPushed(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := endRig(pu, Config{As: "m", Width: 2})
	p := pk("c1")
	p.Branch = ""
	startWork(t, g, &p)
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.reset()
	_, err := g.tick(t)
	require.NoError(t, err)
	fin := g.s.lines("finish")
	require.Len(t, fin, 1, g.out.String())
	assert.Contains(t, fin[0], "the packet names no branch to push to")
	assert.Empty(t, pu.cards())
}

// A child done while the queue shows its card ready is not finished and the launch
// stays (member.go, endEnded's working/reading column guard).
func TestAChildDoneWithItsCardReadyIsNotFinished(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := endRig(pu, Config{As: "m", Width: 2})
	p := pk("c1")
	startWork(t, g, &p)
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, queueCard{ID: "c1", Col: "ready", Gen: 1}))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, g.s.lines("finish"))
	assert.Equal(t, 1, g.m.Running(), "the launch stays")
}

// A child done whose card the queue shows at the next generation is reaped, never
// pushed or finished from the old launch (member.go, endEnded's moved guard).
func TestAChildDoneWithItsClaimMovedIsNotPushed(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := endRig(pu, Config{As: "m", Width: 2})
	p := pk("c1")
	startWork(t, g, &p)
	next := pk("c1")
	next.Gen = 2
	// a pass that sees the claim moved while the child still runs remembers it
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &next)))
	g.s.reset()
	_, err := g.tick(t)
	require.NoError(t, err)
	// the child ends under the old claim
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &next)))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, pu.cards(), "no push from the old launch")
	assert.Empty(t, g.s.lines("finish"))
	assert.Contains(t, g.out.String(), "claim moved")
}

// A reader child that ended is reported as a read and never pushed (member.go,
// endEnded's reader branch).
func TestAReaderChildEndedIsOneReadReportAndNoPush(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := endRig(pu, Config{As: "r", Width: 1, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Primary: "p1", Attempt: 1, Gen: 1, Epoch: 7}
	g.s.set("queue", 0, queueJSON(t, 7, asked("r1", &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	c := g.r.child("r1")
	require.NotNil(t, c, g.out.String())
	c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Report: "looked"})
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Len(t, g.s.lines("report"), 1, g.out.String())
	assert.Empty(t, pu.cards())
}

// A finish the store did not answer is reported again on the next pass and the pusher
// is called once: a launch keeps its push (member.go, endEnded's comment; reportOne's
// code 2 branch).
func TestARefusedFinishIsReportedAgainAndPushedOnce(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := endRig(pu, Config{As: "m", Width: 2})
	p := pk("c1")
	startWork(t, g, &p)
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.set("finish", 2, "")
	g.s.reset()
	_, err := g.tick(t)
	require.Error(t, err, "the store did not answer")
	require.Len(t, g.s.lines("finish"), 1, g.out.String())
	require.Len(t, pu.cards(), 1)
	g.s.set("finish", 2, "")
	g.s.reset()
	_, err = g.tick(t)
	require.Error(t, err)
	assert.Len(t, g.s.lines("finish"), 1, "the finish is reported again")
	assert.Len(t, pu.cards(), 1, "never pushed again")
}

// A work child whose card left the queue and came back at the same claim is still
// finished on the queue path (member.go, endEnded's body; endEndedLocal leaves a
// dropped launch alone).
func TestAWorkChildWhoseCardReturnedToTheQueueIsFinished(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := endRig(pu, Config{As: "m", Width: 2})
	p := pk("c1")
	startWork(t, g, &p)
	// a pass with the card gone from the queue marks the launch dropped
	g.s.set("queue", 0, queueJSON(t, 7))
	g.s.reset()
	_, err := g.tick(t)
	require.NoError(t, err)
	// the child ends; the card is back in the queue at the same claim
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"c1"}, pu.cards(), "the queue path pushes the returned card: %s", g.out.String())
	assert.Len(t, g.s.lines("finish"), 1, g.out.String())
}

// Nine work children ended at once are pushed at most pushWidth at a time (member.go,
// endEnded's pushGate).
func TestAtMostPushWidthPushesRunAtOnce(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}, yield: 500}
	g := endRig(pu, Config{As: "m", Width: 10})
	cards := make([]queueCard, 0, 9)
	packets := make([]*Packet, 0, 9)
	for i := 0; i < 9; i++ {
		p := pk("c" + string(rune('1'+i)))
		p.Branch = "work/" + p.Card
		packets = append(packets, &p)
		cards = append(cards, working(p.Card, 1, &p))
	}
	g.s.set("queue", 0, queueJSON(t, 7, cards...))
	_, err := g.tick(t)
	require.NoError(t, err)
	for _, p := range packets {
		c := g.r.child(p.Card)
		require.NotNil(t, c, "recovered %s: %s", p.Card, g.out.String())
		c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	}
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, 9, len(pu.cards()), g.out.String())
	assert.LessOrEqual(t, int(pu.most.Load()), pushWidth, "no more than pushWidth pushes in flight")
	assert.Greater(t, int(pu.most.Load()), 1, "the pushes ran side by side")
}
