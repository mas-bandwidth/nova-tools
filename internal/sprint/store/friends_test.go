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
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "bob", true, "c"))
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// up first, then held (FleetOrder); the counts are all zero, never read
	assert.Equal(t, []FriendRow{
		{Name: "amy", Width: 3, Status: sprint.Up},
		{Name: "bob", Width: 1, Status: sprint.Held},
	}, rows)
}

func TestStoreFriendTakeFencingAndHoldReclaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()

	// Sync friend amy (width 1, tiers: flash)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	// Add friend cards
	h.must(AddStep(sprint.AddReq{
		Stream: "s1",
		Cards: []sprint.CardAdd{
			{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"},
			{ID: "s1-2", Brief: "c: card 2\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 2"},
		},
	}))

	// Initial beat so FriendDeal sees her Up and stages card to ready reserve
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)

	// Machine tick 1: work pump drains queued cards into Work table Ready
	h.machine()
	// Machine tick 2: deal stages s1-1 into friend.amy ready reserve (width 1)
	h.machine()
	require.NotNil(t, h.snap().Fleet.Card("s1-1.w1"))
	require.Equal(t, sprint.Ready, h.snap().Fleet.Card("s1-1.w1").Col)
	require.Equal(t, sprint.Ready, h.snap().StateOf("s1-2"), "s1-2 waits in backlog since reserve room is 1")

	// 1. Advance clock past beat timeout (15s): Amy is Down
	h.tick(20 * time.Second)
	takeRes := h.run(TakeStep(sprint.TakeReq{
		Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
		As:   sprint.FriendRow("amy"),
		Gens: map[string]int{"s1-1.w1": 1},
		Who:  sprint.FriendRow("amy"),
	}))
	require.Len(t, takeRes.Refused, 1)
	assert.Contains(t, takeRes.Refused[0].Why, "down")

	// 2. Amy beats: now Up
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)

	// 3. Take succeeds: ready -> working
	h.must(TakeStep(sprint.TakeReq{
		Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
		As:   sprint.FriendRow("amy"),
		Gens: map[string]int{"s1-1.w1": 1},
		Who:  sprint.FriendRow("amy"),
	}))
	require.Equal(t, sprint.Working, h.snap().Fleet.Card("s1-1.w1").Col)

	// 4. Tick refills reserve with s1-2!
	h.machine()
	require.Equal(t, sprint.Ready, h.snap().Fleet.Card("s1-2.w1").Col)

	// 5. Try to take s1-2.w1: Amy is already at active width (1 working). Refused!
	takeRes2 := h.run(TakeStep(sprint.TakeReq{
		Sel:  sprint.Sel{IDs: []string{"s1-2.w1"}},
		As:   sprint.FriendRow("amy"),
		Gens: map[string]int{"s1-2.w1": 1},
		Who:  sprint.FriendRow("amy"),
	}))
	require.Len(t, takeRes2.Refused, 1)
	assert.Contains(t, takeRes2.Refused[0].Why, "active width")

	// 6. Coordinator holds Amy: SetFriendHeld atomically reclaims working and ready tasks!
	err = h.st.SetFriendHeld(h.ctx, "amy", true, "glenn")
	require.NoError(t, err)

	// Both cards are withdrawn in Fleet, generation bumped, without penalty
	c1 := h.snap().Fleet.Card("s1-1.w1")
	c2 := h.snap().Fleet.Card("s1-2.w1")
	require.NotNil(t, c1)
	require.NotNil(t, c2)
	assert.Equal(t, sprint.Withdrawn, c1.Col)
	assert.Equal(t, sprint.Withdrawn, c2.Col)
	assert.Equal(t, "2", c1.F("gen"))
	assert.Equal(t, "2", c2.F("gen"))
	assert.Empty(t, c1.F(sprint.FieldTakeEnded))
	assert.Empty(t, c2.F(sprint.FieldTakeEnded))

	// Primaries return to Ready in Work table
	assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-1"))
	assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-2"))

	// Taking while held is refused
	takeRes3 := h.run(TakeStep(sprint.TakeReq{
		Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
		As:   sprint.FriendRow("amy"),
		Gens: map[string]int{"s1-1.w1": 2},
		Who:  sprint.FriendRow("amy"),
	}))
	require.Len(t, takeRes3.Refused, 1)
	assert.Contains(t, takeRes3.Refused[0].Why, "held")

	// Roster reflects Held: true
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Held, rows[0].Status)
}

// TestFriendHoldCrashReplay proves that a hold operation acquired in the fence but
// interrupted before Release is durably committed by store recovery/replay.
func TestFriendHoldCrashReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)

	// Verify initially Up
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Equal(t, sprint.Up, rows[0].Status)

	// Simulate crash: Acquire fence with FriendHold, apply manifests, but DO NOT Release
	snap, gen, err := h.st.Fenced(h.ctx, tables(sprint.Fleet, sprint.Work), nil, nil)
	require.NoError(t, err)
	snap.Friends = []sprint.FriendSeat{{Name: "amy", Width: 2, Status: sprint.Up, Tiers: []string{"flash"}}}
	plan := sprint.Applied(snap, sprint.FriendHold(snap, "amy", "glenn"))
	require.NotNil(t, plan.Roster)

	op, err := h.st.operation("friend down", "glenn", "crash-hold-op-1", plan, snap)
	require.NoError(t, err)
	ok, err := h.st.B.Acquire(h.ctx, gen, op)
	require.True(t, ok)
	require.NoError(t, err)

	applied, _, err := h.st.apply(h.ctx, op)
	require.True(t, applied)
	require.NoError(t, err)

	// Verify fence holds pending operation
	f, err := h.st.B.ReadFence(h.ctx)
	require.NoError(t, err)
	require.NotNil(t, f.Pending)
	require.Equal(t, "crash-hold-op-1", f.Pending.ID)

	// Next fenced store access triggers repair/finish of the pending hold
	var repaired []string
	_, _, err = h.st.Fenced(h.ctx, tables(sprint.Fleet), nil, &repaired)
	require.NoError(t, err)
	require.NotEmpty(t, repaired)

	// Verify the roster hold was durably committed during replay
	rowsAfter, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rowsAfter, 1)
	assert.Equal(t, sprint.Held, rowsAfter[0].Status)
}

// TestSyncFriendsPreservesLiveHoldState proves that SyncFriends never overwrites
// an active friend hold when syncing config specs.
func TestSyncFriendsPreservesLiveHoldState(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	// Hold Amy
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))

	// Verify Amy is Held
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Equal(t, sprint.Held, rows[0].Status)

	// SyncFriends runs with updated width and tiers
	added, removed, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "amy", Width: 4, Tiers: []string{"flash", "pro"}},
	})
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Empty(t, removed)
	assert.Equal(t, []string{"amy"}, updated)

	// Amy MUST still be Held, with her updated width and tiers preserved!
	rowsAfter, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rowsAfter, 1)
	assert.Equal(t, sprint.Held, rowsAfter[0].Status)
	assert.Equal(t, 4, rowsAfter[0].Width)
	assert.Equal(t, []string{"flash", "pro"}, rowsAfter[0].Tiers)

	// And when friend up is called, she is released cleanly
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", false, "glenn"))
	rowsReleased, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, rowsReleased[0].Status) // Down until she beats
	assert.Equal(t, 4, rowsReleased[0].Width)
}
