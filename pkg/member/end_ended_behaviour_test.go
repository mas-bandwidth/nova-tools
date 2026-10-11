package member

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// armChild is a child that only reports done once the queue has been read: the pass's
// local end (endEndedLocal, before the queue) sees it not done, and the queue read arms
// it so the pass's own end (endEnded, after the queue) sees it done. It reaches the
// endEnded branch this card covers without touching product code.
type armChild struct {
	mu    sync.Mutex
	ended bool
	armed bool
	res   Result
}

func (c *armChild) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended && c.armed
}

func (c *armChild) Result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.res
}

func (c *armChild) end(r Result) {
	c.mu.Lock()
	c.ended, c.res = true, r
	c.mu.Unlock()
}

func (c *armChild) armNow() {
	c.mu.Lock()
	if c.ended {
		c.armed = true
	}
	c.mu.Unlock()
}

// armRunner starts armChilds, keyed by card.
type armRunner struct {
	mu       sync.Mutex
	children map[string]*armChild
}

func newArmRunner() *armRunner { return &armRunner{children: map[string]*armChild{}} }

func (r *armRunner) Start(p Packet) (Child, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := &armChild{}
	r.children[p.Card] = c
	return c, nil
}

func (r *armRunner) Ended(Packet, bool) {}

func (r *armRunner) child(id string) *armChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.children[id]
}

func (r *armRunner) all() []*armChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*armChild, 0, len(r.children))
	for _, c := range r.children {
		out = append(out, c)
	}
	return out
}

// armSprint arms every started child on the queue read, after endEndedLocal saw them
// not done and before endEnded asks.
type armSprint struct {
	*scriptSprint
	runner *armRunner
}

func (s *armSprint) Run(args ...string) (int, []byte) {
	if verbOf(args) == "queue" {
		for _, c := range s.runner.all() {
			c.armNow()
		}
	}
	return s.scriptSprint.Run(args...)
}

func newArmRig(cfg Config, pu Pusher) (*rig, *armRunner) {
	inner := newScript()
	ar := newArmRunner()
	as := &armSprint{scriptSprint: inner, runner: ar}
	out := &bytes.Buffer{}
	g := &rig{m: New(cfg, as, ar, pu, out), s: inner, out: out}
	return g, ar
}

// beginArm starts id from the queue at gen p.Gen and returns its armChild.
func beginArm(t *testing.T, g *rig, ar *armRunner, id string, p Packet) *armChild {
	t.Helper()
	g.s.set("queue", 0, queueJSON(t, 7, working(id, p.Gen, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	c := ar.child(id)
	require.NotNil(t, c, "the child was started")
	return c
}

// The end of a launch (member.go endEnded; docs/SPEC-SWARM.md, member; docs/SPEC-SPRINT.md,
// the finish row): a work child whose card the queue still holds at its claim is finished.

// A member with no pusher is finished failed, no commit, with that reason named
// (member.go:1942).
func TestAMemberWithNoPusherFinishesNoCommitNamingNoPusher(t *testing.T) {
	t.Parallel()
	g, ar := newArmRig(Config{As: "m", Width: 1}, nil)
	p := pk("c1")
	c := beginArm(t, g, ar, "c1", p)
	c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "done"})
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1, g.out.String())
	assert.Contains(t, finish[0], "no commit: this member has no pusher; done")
}

// A packet that names no branch finishes failed, no commit, naming the packet
// (member.go:1940), and the pusher is never asked.
func TestAPacketNamingNoBranchFinishesNoCommitNamingThePacket(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, ar := newArmRig(Config{As: "m", Width: 1}, pu)
	p := pk("c1")
	p.Branch = ""
	c := beginArm(t, g, ar, "c1", p)
	c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "done"})
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1, g.out.String())
	assert.Contains(t, finish[0], "no commit: the packet names no branch to push to; done")
	assert.Empty(t, pu.cards(), "a packet with no branch is never pushed")
}

// A child done while the queue shows its card at another column (ready) is not the
// pass's end: no finish verb, and the launch stays (member.go:1918).
func TestAChildDoneWhileItsCardIsReadyIsNotFinished(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, ar := newArmRig(Config{As: "m", Width: 1}, pu)
	p := pk("c1")
	c := beginArm(t, g, ar, "c1", p)
	c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "done"})
	g.s.reset()
	// the card is back in a column the end does not act on, at the same claim
	g.s.set("queue", 0, queueJSON(t, 7, queueCard{ID: "c1", Col: "ready", Gen: 1}))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, g.s.lines("finish"), "a card the queue does not show working is not finished: %s", g.out.String())
	assert.Empty(t, pu.cards())
	assert.Equal(t, 1, g.m.Running(), "the launch stays, holding its lane")
}

// A child done while the queue shows its card at the next generation (the claim moved)
// is not the old launch's: no push and no finish from it (member.go:1918, m.moved).
func TestAChildDoneWhenTheClaimMovedIsNotFinishedFromTheOldLaunch(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, ar := newArmRig(Config{As: "m", Width: 1}, pu)
	p := pk("c1")
	c := beginArm(t, g, ar, "c1", p)
	c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "done"})
	g.s.reset()
	next := pk("c1")
	next.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &next)))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, g.s.lines("finish"), "the old launch is not finished after a move: %s", g.out.String())
	assert.Empty(t, pu.cards())
}

// A reader child's end is one read report and no push (member.go:1930).
func TestAReaderChildEndedIsOneReadReportAndNoPush(t *testing.T) {
	t.Parallel()
	g, ar := newArmRig(Config{As: "r", Width: 1, Reader: true}, nil)
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	c := beginArm(t, g, ar, "r1", p)
	c.end(Result{Ran: true, OK: true, Verdict: "ok", Head: fullSha, Report: "clean"})
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"read --as r --ok r1 --finding clean --epoch 7"}, g.s.lines("report"))
	assert.Empty(t, g.s.lines("finish"))
	assert.NotContains(t, g.out.String(), "push")
}

// A finish the store refused is reported again on the next tick, and the pusher was
// called once across both (member.go:1909).
func TestAFinishTheStoreRefusedIsReportedAgainWithoutPushingAgain(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, ar := newArmRig(Config{As: "m", Width: 1}, pu)
	p := pk("c1")
	c := beginArm(t, g, ar, "c1", p)
	c.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	g.s.set("finish", 2, "no route to the store")
	_, err := g.tick(t)
	require.Error(t, err, "the store did not answer the finish")
	require.Len(t, g.s.lines("finish"), 1)
	g.s.set("finish", 0, "")
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Len(t, g.s.lines("finish"), 2, "the finish is reported again")
	assert.Equal(t, []string{"c1"}, pu.cards(), "the pusher is called once across both ticks")
}

// Nine work children ended at once are pushed side by side, never more than pushWidth
// at a moment (member.go:1944, the push width).
func TestNineWorkChildrenEndedAtOnceNeverExceedThePushWidth(t *testing.T) {
	t.Parallel()
	ids := make([]string, 9)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%02d", i)
	}
	pu := &fakePusher{def: Push{Sha: fullSha}, yield: 2000}
	g, ar := newArmRig(Config{As: "m", Width: len(ids)}, pu)
	var cards []queueCard
	for _, id := range ids {
		p := pk(id)
		cards = append(cards, working(id, 1, &p))
	}
	g.s.set("queue", 0, queueJSON(t, 7, cards...))
	_, err := g.tick(t)
	require.NoError(t, err)
	for _, id := range ids {
		ar.child(id).end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "r"})
	}
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, cards...))
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Greater(t, pu.most.Load(), int32(1), "the pushes ran side by side")
	assert.LessOrEqual(t, pu.most.Load(), int32(pushWidth), "never more than pushWidth pushes at once")
	assert.ElementsMatch(t, ids, pu.cards())
}
