package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FriendRows is the roster with width and status only: a friend's ready,
// working, ok and failed are her sprint cards' counts, filled by where from
// her fleet row (friend.<name>), never read or counted here — the store holds
// no job record.
func TestFriendRowsReturnsNameWidthStatusOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}, {Name: "bob", Width: 1}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "bob", true, "c", "", time.Time{}, 0))
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// up first, then held (FleetOrder); the counts are all zero, never read; her beat's time
	// is carried (view coordinator reads how stale her report is), and the evidence her
	// status rests on, her session's pong, never her beat
	assert.Equal(t, []FriendRow{
		{Name: "amy", Width: 3, Status: sprint.Up, Evidence: "session pong 0s ago", Beat: h.now.UTC().Truncate(time.Second), Health: &sprint.FriendHealth{State: sprint.Up, Seen: h.now, Generation: 1}},
		{Name: "bob", Width: 1, Status: sprint.Held, Evidence: "held"},
	}, rows)
}

// up is a friend's beat and a wake ping her session answered, observed by the seat's
// holder at the clock now: a friend up, as the tests that deal to her want her.
func (h *harness) up(friend string) {
	h.t.Helper()
	_, err := h.st.FriendBeat(h.ctx, friend)
	require.NoError(h.t, err)
	_, _, _, err = h.health(friend, "tester", sprint.Up, h.now, 1)
	require.NoError(h.t, err)
}

// A friend's delivery mode (batch or one-shot) is respected by the store's tick dealing on
// its twin (docs/SPEC-SPRINT.md section 1, "A friend's card"): in either mode she fills up to
// her width working and ready behind (DealAhead times width); a one-shot friend runs one lane
// per unit of width (the owner, 2026-10-10), and the lane a finish frees takes her next at once.
func TestTwinStoreDealingRespectsFriendDeliveryMode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "amy", Width: 2, Mode: "batch", Class: "flash"},
		{Name: "bob", Width: 2, Mode: "one-shot", Class: "flash"},
	})
	require.NoError(t, err)
	h.up("amy")
	h.up("bob")

	brief := func(who string) string {
		return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-1", Brief: brief("only friend amy")},
		{ID: "s1-2", Brief: brief("only friend amy")},
		{ID: "s1-3", Brief: brief("only friend amy")},
		{ID: "s1-4", Brief: brief("only friend amy")},
		{ID: "s1-5", Brief: brief("only friend bob")},
		{ID: "s1-6", Brief: brief("only friend bob")},
		{ID: "s1-7", Brief: brief("only friend bob")},
	}}))

	h.startMachine()
	h.machine()
	h.start("amy", 2) // each starts what her lanes hold: dealt ready, working once started
	h.start("bob", 2)

	snap := h.snap()
	amyRow := sprint.FriendRow("amy")
	bobRow := sprint.FriendRow("bob")
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Working))
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Ready))
	// Bob (one-shot mode, width 2): his three cards dealt, 2 working and 1 ready behind
	assert.Equal(t, 2, snap.Fleet.Count(bobRow, sprint.Working))
	assert.Equal(t, 1, snap.Fleet.Count(bobRow, sprint.Ready))

	// Bob finishes a card: the lane it frees takes his next at once
	h.must(FinishStep(sprint.FinishReq{
		As: bobRow, Sel: sprint.Sel{IDs: []string{"s1-5.w1"}},
		Gens: map[string]int{"s1-5.w1": 1}, Head: "abc",
	}))
	snap = h.snap()
	assert.Equal(t, 2, snap.Fleet.Count(bobRow, sprint.Working), "the freed lane took his next")
	assert.Equal(t, 0, snap.Fleet.Count(bobRow, sprint.Ready))
}

// A friend's row switching from batch to one-shot mode through config sync keeps her lanes:
// a one-shot friend runs one lane per unit of width (the owner, 2026-10-10), so each finish
// frees its own lane and her oldest ready card is taken into it at once.
func TestTwinStoreConfigSyncToOneShotKeepsEachLaneRefreshing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Amy begins in batch mode with width 2
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "amy", Width: 2, Mode: "batch", Class: "flash"},
	})
	require.NoError(t, err)
	h.up("amy")

	brief := func(who string) string {
		return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-1", Brief: brief("only friend amy")},
		{ID: "s1-2", Brief: brief("only friend amy")},
		{ID: "s1-3", Brief: brief("only friend amy")},
		{ID: "s1-4", Brief: brief("only friend amy")},
	}}))

	h.startMachine()
	h.machine()
	h.start("amy", 2) // she starts what her lanes hold

	snap := h.snap()
	amyRow := sprint.FriendRow("amy")

	// Amy has 2 working, 2 ready behind
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Working))
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Ready))
	assert.Equal(t, sprint.Working, snap.StateOf("s1-1"))
	assert.Equal(t, sprint.Working, snap.StateOf("s1-2"))

	// Switch amy to one-shot mode through config sync
	_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "amy", Width: 2, Mode: "one-shot", Class: "flash"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"amy"}, updated)

	// Finish the first Working card (s1-1.w1)
	h.must(FinishStep(sprint.FinishReq{
		As: amyRow, Sel: sprint.Sel{IDs: []string{"s1-1.w1"}},
		Gens: map[string]int{"s1-1.w1": 1}, Head: "abc",
	}))

	snap = h.snap()
	// s1-2.w1 still runs and the lane s1-1 freed took s1-3.w1 at once
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Working))
	assert.Equal(t, 1, snap.Fleet.Count(amyRow, sprint.Ready))
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, sprint.Ready, snap.Fleet.Card("s1-4.w1").Col)

	h.must(FinishStep(sprint.FinishReq{
		As: amyRow, Sel: sprint.Sel{IDs: []string{"s1-2.w1"}},
		Gens: map[string]int{"s1-2.w1": 1}, Head: "def",
	}))
	snap = h.snap()
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Working), "the next freed lane took s1-4")
	assert.Equal(t, 0, snap.Fleet.Count(amyRow, sprint.Ready))
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-4.w1").Col)
}
