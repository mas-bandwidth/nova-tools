package member

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// onQueueSprint is a scriptSprint that runs a hook the first time the member reads the
// queue in a pass. endEnded runs after endEndedLocal, so a child that is already done
// before a pass is taken by endEndedLocal; a child that ends in the window between the
// two (here, while the queue is being read) is the one endEnded (member.go:1913) sees.
type onQueueSprint struct {
	*scriptSprint
	mu   sync.Mutex
	hook func()
}

func (s *onQueueSprint) Run(args ...string) (int, []byte) {
	if len(args) > 0 && verbOf(args) == "queue" {
		s.mu.Lock()
		hook := s.hook
		s.hook = nil
		s.mu.Unlock()
		if hook != nil {
			hook()
		}
	}
	return s.scriptSprint.Run(args...)
}

// endedRig builds a member with one work card c1 at gen 1 already ours, then arms the
// sprint to end its child while the next pass reads the queue, so the end reaches
// endEnded. The queue card's column is read from the answer set before the pass.
func endedRig(t *testing.T, width int, cfg Config, pu Pusher, id string, r Result) (*rig, *onQueueSprint) {
	t.Helper()
	s0 := newScript()
	s := &onQueueSprint{scriptSprint: s0}
	rn, out := newRunner(), &bytes.Buffer{}
	g := &rig{m: New(cfg, rowWidth(s, width), rn, pu, out), s: s0, r: rn, out: out}
	p := pk(id)
	s0.set("queue", 0, queueJSON(t, 7, working(id, 1, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.NotNil(t, g.r.child(id), "the launch started")
	s.hook = func() { g.r.child(id).end(r) }
	s0.reset()
	return g, s
}

// A work child that ended with a head and no pusher is finished no commit, naming the
// absent pusher: "this member has no pusher" (member.go:1942).
func TestAWorkChildThatEndedWithNoPusherIsFinishedNoCommit(t *testing.T) {
	t.Parallel()
	g, _ := endedRig(t, 1, Config{As: "m"}, nil, "c1",
		Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done"})
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1, g.out.String())
	assert.Contains(t, finish[0], "no commit: this member has no pusher")
}

// A work child whose packet names no branch is finished no commit: "the packet names no
// branch to push to" (member.go:1940).
func TestAWorkChildWhosePacketNamesNoBranchIsFinishedNoCommit(t *testing.T) {
	t.Parallel()
	s0 := newScript()
	s := &onQueueSprint{scriptSprint: s0}
	rn, out := newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "m"}, rowWidth(s, 1), rn, &fakePusher{def: Push{Sha: fullSha}}, out), s: s0, r: rn, out: out}
	p := pk("c1")
	p.Branch = ""
	s0.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	s.hook = func() {
		g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done"})
	}
	s0.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1, g.out.String())
	assert.Contains(t, finish[0], "no commit: the packet names no branch to push to")
}

// A child done while the queue shows its card ready (not working or reading) is not
// finished, and the launch stays (member.go:1918, the column test).
func TestAChildDoneWhileItsCardIsReadyIsNotFinished(t *testing.T) {
	t.Parallel()
	g, s := endedRig(t, 1, Config{As: "m"}, &fakePusher{def: Push{Sha: fullSha}}, "c1",
		Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done"})
	p := pk("c1")
	s.scriptSprint.set("queue", 0, queueJSON(t, 7, queueCard{ID: "c1", Col: "ready", Gen: 1, Packet: &p}))
	s.scriptSprint.set("take", 0, takeJSON(t))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, g.s.lines("finish"), "a card not in working or reading is not finished")
	assert.Equal(t, 1, g.m.Running(), "the launch stays")
}

// A child done while the queue shows its card at the next generation (the claim moved)
// is not pushed and not finished from the old launch (member.go:1918, moved).
func TestAChildDoneWhenTheClaimMovedIsNotFinished(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, s := endedRig(t, 1, Config{As: "m"}, pu, "c1",
		Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done"})
	s.scriptSprint.set("queue", 0, queueJSON(t, 7, queueCard{ID: "c1", Col: "working", Gen: 2}))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, g.s.lines("finish"), "the old launch is not finished")
	assert.Empty(t, pu.cards(), "the old launch is not pushed")
}

// A reader child that ended posts one read report and no push (member.go:1930-1931).
func TestAReaderChildEndedPostsOneReadReportAndNoPush(t *testing.T) {
	t.Parallel()
	s0 := newScript()
	s := &onQueueSprint{scriptSprint: s0}
	rn, out := newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "r", Reader: true}, rowWidth(s, 1), rn, nil, out), s: s0, r: rn, out: out}
	a := Packet{Card: "r1", Kind: "read", As: "r", Primary: "p1", Attempt: 1, Epoch: 7}
	s0.set("queue", 0, queueJSON(t, 7, queueCard{ID: "r1", Col: "reading", Packet: &a}))
	_, err := g.tick(t)
	require.NoError(t, err)
	s.hook = func() { g.r.child("r1").end(Result{Ran: true, OK: true, Verdict: "ok", Report: "clean"}) }
	s0.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"read --as r --ok r1 --finding clean --epoch 7"}, g.s.lines("report"))
	assert.NotContains(t, g.out.String(), "push")
}

// A finish the store refused is reported again on the next pass, from the push already
// made: the pusher is called once (member.go:1910, "never pushed again").
func TestAFinishTheStoreRefusedIsReportedAgainAndPushedOnce(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, s := endedRig(t, 1, Config{As: "m"}, pu, "c1",
		Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "r"})
	s.scriptSprint.set("finish", 2, "no route to the store")
	_, err := g.tick(t)
	require.Error(t, err)
	s.scriptSprint.set("finish", 0, "")
	acted, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, 1, acted)
	assert.Equal(t, []string{"c1"}, pu.cards(), "pushed once across the two passes")
	finish := g.s.lines("finish")
	require.Len(t, finish, 2)
	assert.Equal(t, finish[0], finish[1], "the second finish is the first, again")
}

// Nine work children ended at once are pushed side by side, never more than pushWidth
// in flight at any moment (member.go:1944-1946, pushWidth).
func TestNineChildrenEndedAtOncePushAtMostPushWidth(t *testing.T) {
	t.Parallel()
	const n = pushWidth + 1
	s0 := newScript()
	s := &onQueueSprint{scriptSprint: s0}
	pu := &fakePusher{def: Push{Sha: fullSha}, yield: 2000}
	rn, out := newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "m"}, rowWidth(s, n), rn, pu, out), s: s0, r: rn, out: out}
	var cards []queueCard
	var ids []string
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("c%02d", i)
		p := pk(id)
		cards = append(cards, working(id, 1, &p))
		ids = append(ids, id)
	}
	s0.set("queue", 0, queueJSON(t, 7, cards...))
	_, err := g.tick(t)
	require.NoError(t, err)
	s.hook = func() {
		for _, id := range ids {
			g.r.child(id).end(Result{Ran: true, OK: true, Head: fullSha, Report: "r"})
		}
	}
	s0.reset()
	acted, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, n, acted)
	assert.Greater(t, pu.most.Load(), int32(1), "the pushes ran side by side")
	assert.LessOrEqual(t, pu.most.Load(), int32(pushWidth), "never more than pushWidth pushes at once")
	assert.ElementsMatch(t, ids, pu.cards())
}
