package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMemberRunsReadCardsAtHalfASlotAndReportsThemWithTheReadVerb pins a read card on a
// member's fleet row (docs/SPEC-SPRINT.md section 6, "A read is a consumer card"): the member
// of width 1 asks its take for two half slots when reads are ready and starts both reads at
// once (a read holds half a slot); a read pushes nothing, and its verdict is reported with
// the read verb as the member, not finish.
func TestAMemberRunsReadCardsAtHalfASlotAndReportsThemWithTheReadVerb(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	pusher := &fakePusher{def: Push{Sha: fullSha}}
	g.m.pusher = pusher
	a := Packet{Card: "p1.r1.m", Kind: "read", As: "m", Primary: "p1", Attempt: 1, Gen: 1, Epoch: 7, Head: fullSha}
	b := Packet{Card: "p2.r1.m", Kind: "read", As: "m", Primary: "p2", Attempt: 1, Gen: 1, Epoch: 7, Head: fullSha}
	g.s.set("queue", 0, queueJSON(t, 7, ready(a.Card), ready(b.Card)))
	g.s.set("take", 0, takeJSON(t, a, b))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"take --as m --limit 2 --json --epoch 7"}, g.s.lines("take"), "two half slots")
	require.Equal(t, []string{a.Card, b.Card}, g.r.started(), "both reads run at once at width 1")

	g.r.child(a.Card).end(Result{Ran: true, OK: true, Verdict: "ok", Report: "clean"})
	g.r.child(b.Card).end(Result{Ran: true, OK: false, Verdict: "broken", Report: "## Finding\nmain.go:12: the merge is wrong"})
	g.s.set("queue", 0, queueJSON(t, 7, working(a.Card, 1, &a), working(b.Card, 1, &b)))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{
		"read --as m --ok p1.r1.m --finding clean --epoch 7",
		"read --as m --broken p2.r1.m --finding main.go:12: the merge is wrong --epoch 7",
	}, g.s.lines("report"))
	require.Empty(t, g.s.lines("finish"), "a read is not finished")
	pusher.mu.Lock()
	require.Empty(t, pusher.asked, "a read pushes nothing")
	pusher.mu.Unlock()
	require.Equal(t, 0, g.m.Running())
}
