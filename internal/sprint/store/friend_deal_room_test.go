package store

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The store's tick deals a card for any friend by room within a class and levels the friends
// after its deal, reading what each has started from the store alone (docs/SPEC-SPRINT.md
// section 1, friend-deal-most-room-now.w1).

// roomBrief is a friend's card's brief, its WHO line who.
func roomBrief(who string) string {
	return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

// fullAmy is a harness whose friend amy, width 8, holds her room after one tick: sixteen
// cards of stream s1 whose WHO line is who, eight working and eight ready on her row.
func fullAmy(t *testing.T, who string) *harness {
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 8, Class: "flash,pro"}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	var cards []sprint.CardAdd
	for i := range 16 {
		cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("s1-%d", i+1), Brief: roomBrief(who)})
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: cards}))
	h.startMachine()
	h.machine()
	snap := h.snap()
	require.Equal(t, 8, snap.Fleet.Count(sprint.FriendRow("amy"), sprint.Working))
	require.Equal(t, 8, snap.Fleet.Count(sprint.FriendRow("amy"), sprint.Ready))
	return h
}

// joinFriends syncs amy and the friends named (width each, amy's class) and beats them up.
func joinFriends(h *harness, width int, names ...string) {
	h.t.Helper()
	specs := []FriendSpec{{Name: "amy", Width: 8, Class: "flash,pro"}}
	for _, n := range names {
		specs = append(specs, FriendSpec{Name: n, Width: width, Class: "flash,pro"})
	}
	_, _, _, err := h.st.SyncFriends(h.ctx, specs)
	require.NoError(h.t, err)
	for _, n := range names {
		_, err = h.st.FriendBeat(h.ctx, n)
		require.NoError(h.t, err)
	}
}

func TestTwinStoreFriendDealByRoomAndLevelEveryTick(t *testing.T) {
	t.Parallel()
	amy, bob, cat := sprint.FriendRow("amy"), sprint.FriendRow("bob"), sprint.FriendRow("cat")

	t.Run("five new friend cards go to the idle two by room, none to the full one", func(t *testing.T) {
		t.Parallel()
		h := fullAmy(t, "friend amy")
		joinFriends(h, 4, "bob", "cat")
		var cards []sprint.CardAdd
		for i := range 5 {
			cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("s2-%d", i+1), Brief: roomBrief("friend")})
		}
		h.must(AddStep(sprint.AddReq{Stream: "s2", Cards: cards}))
		h.machine()
		snap := h.snap()
		// rooms amy 0, bob 8, cat 8: bob first by name among equals, then the one with more room
		for i, want := range []string{bob, cat, bob, cat, bob} {
			wc := snap.Fleet.Card(fmt.Sprintf("s2-%d.w1", i+1))
			require.NotNil(t, wc, "s2-%d dealt", i+1)
			assert.Equal(t, want, wc.Row, "s2-%d", i+1)
		}
		assert.Equal(t, 8, snap.Fleet.Count(amy, sprint.Working))
		assert.Equal(t, 8, snap.Fleet.Count(amy, sprint.Ready), "amy at her room is dealt nothing")
	})

	t.Run("a tick evens a backlog without a verb, and a card her beat names running stays", func(t *testing.T) {
		t.Parallel()
		h := fullAmy(t, "friend")
		// amy alone: nobody to level to, the next tick moves nothing
		h.machine()
		require.Equal(t, 16, h.snap().Fleet.Count(amy, sprint.Working)+h.snap().Fleet.Count(amy, sprint.Ready))
		_, err := h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-16.w1"}}, nil)
		require.NoError(t, err)
		joinFriends(h, 8, "bob")
		h.machine()
		snap := h.snap()
		// backlogs 8 and -8 even to 1 and -1: seven move, the one her beat names running stays
		assert.Equal(t, 9, snap.Fleet.Count(amy, sprint.Working)+snap.Fleet.Count(amy, sprint.Ready))
		assert.Equal(t, 7, snap.Fleet.Count(bob, sprint.Working), "bob had his lanes free")
		assert.Equal(t, amy, snap.Fleet.Card("s1-16.w1").Row)
		h.machine()
		after := h.snap()
		assert.Equal(t, 9, after.Fleet.Count(amy, sprint.Working)+after.Fleet.Count(amy, sprint.Ready), "even: the next tick moves nothing")
		assert.Equal(t, 7, after.Fleet.Count(bob, sprint.Working)+after.Fleet.Count(bob, sprint.Ready))
	})

	t.Run("started is her beat naming a card running or its progress stamp", func(t *testing.T) {
		t.Parallel()
		h := fullAmy(t, "friend")
		snap := h.snap()
		wc := snap.Fleet.Card("s1-16.w1")
		require.Equal(t, sprint.Ready, wc.Col)
		wc.Fields[sprint.FieldProgress] = "2026-10-04T15:57:00Z"
		// her beat names s1-14's primary running; s1-16.w1 is stamped; the rest are not started
		got := sprint.FriendStartedOf(snap, "amy", []string{"s1-14"})
		assert.Equal(t, map[string]string{
			"s1-14.w1": "her beat names it running",
			"s1-16.w1": "a progress stamp at 2026-10-04T15:57:00Z",
		}, got)
	})
}
