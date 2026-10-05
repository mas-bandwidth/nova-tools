package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The overload alarm covers friends through the store's tick (docs/SPEC-SPRINT.md, "a
// member is overloaded"; the owner: "trust but VERIFY", "Are they actually doing the work
// that is shown in the friend table? Really?"): the tick reads the friend seats whenever the
// roster has a friend (friendSeats), not only when a friend's card is ready, so a friend up
// whose three cards ended on a timeout in the window raises the judgment with nothing
// ready. On the mem twin, the clock injected, no real time.
func TestTheTickRaisesAFriendsOverloadWithNothingReady(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 4}})
	require.NoError(t, err)
	beat := func() {
		t.Helper()
		_, err := h.st.FriendBeat(h.ctx, "amy")
		require.NoError(t, err)
	}
	beat()
	// a friend's card is dealt to her row in working (friend_deal.go): she finishes it, no take
	fail := func(card, report string) {
		t.Helper()
		wc := h.snap().Fleet.Card(card)
		require.NotNil(t, wc)
		gens := map[string]int{wc.ID: wc.Int("gen")}
		h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true, Report: report, Who: wc.Row}))
	}
	h.addReady("s1", 3, "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\nThe task.\n")
	h.startMachine()
	h.machine()
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		require.Equal(t, sprint.FriendRow("amy"), h.snap().Fleet.Card(id).Row, id)
	}
	fail("s1-1.w1", "friend amy HOLD: deadline: the run passed its deadline")
	h.tick(time.Minute)
	beat()
	fail("s1-2.w1", "friend amy FAIL: budget: unverifiable: the usage source stopped answering, tokens 12,345")
	h.tick(time.Minute)
	beat()
	fail("s1-3.w1", "friend amy HOLD: stage-timeout")
	for _, c := range h.snap().Work.Column(sprint.Ready) {
		_, friend := sprint.FriendCard(c)
		require.False(t, friend, "no friend's card is ready: %s", c.ID)
	}
	h.machine()
	open := h.openOf(sprint.NOverloaded)
	require.Len(t, open, 1, "three timeouts within the window, nothing ready")
	n := open[0].Note
	assert.Equal(t, "friend.amy is overloaded: 3 cards ended on a timeout in the last 15m0s: s1-1.w1 (deadline), s1-2.w1 (usage source), s1-3.w1 (stage-timeout); halve its width: nova-config friend set amy --width 2, then nova-sprint friend sync, or wait 15m", n.What)
	assert.Equal(t, []string{"friend set amy --width 2", "wait 15m"}, n.Decisions)
}
