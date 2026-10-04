package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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

// TestFriendHoldZeroCards proves that holding a friend who currently has 0 cards
// still durably commits the roster hold state.
func TestFriendHoldZeroCards(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	// Hold Amy with 0 cards on her row
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))

	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Held, rows[0].Status)

	// Release Amy with 0 cards
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", false, "glenn"))
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Down, rows[0].Status)
}

// TestFriendDuplicateHold proves that duplicate holds or duplicate releases are idempotent.
func TestFriendDuplicateHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))

	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Held, rows[0].Status)

	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", false, "glenn"))
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", false, "glenn"))

	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, rows[0].Status)
}

// TestFriendHoldCrashAfterRelease proves that once an operation executes Release,
// the fence is cleared, the operation is fully committed, and a subsequent fenced
// access does not attempt any pending repair.
func TestFriendHoldCrashAfterRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	snap, gen, err := h.st.Fenced(h.ctx, tables(sprint.Fleet, sprint.Work), nil, nil)
	require.NoError(t, err)
	snap.Friends = []sprint.FriendSeat{{Name: "amy", Width: 2, Status: sprint.Up, Tiers: []string{"flash"}}}
	plan := sprint.Applied(snap, sprint.FriendHold(snap, "amy", "glenn"))
	require.NotNil(t, plan.Roster)

	op, err := h.st.operation("friend down", "glenn", "crash-after-release-op", plan, snap)
	require.NoError(t, err)
	ok, err := h.st.B.Acquire(h.ctx, gen, op)
	require.True(t, ok)
	require.NoError(t, err)

	applied, _, err := h.st.apply(h.ctx, op)
	require.True(t, applied)
	require.NoError(t, err)

	// Release completes the operation and deletes the fence
	require.NoError(t, h.st.B.Release(h.ctx, op, true))

	// Verify fence is clear
	f, err := h.st.B.ReadFence(h.ctx)
	require.NoError(t, err)
	assert.Nil(t, f.Pending)

	// Subsequent fenced read has nothing to repair
	var repaired []string
	_, _, err = h.st.Fenced(h.ctx, tables(sprint.Fleet), nil, &repaired)
	require.NoError(t, err)
	assert.Empty(t, repaired)

	// Roster hold is committed
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Held, rows[0].Status)
}

// TestFriendHoldLostReplyReplay proves that replaying a previously applied
// friend hold operation (lost reply scenario) is safe and idempotent.
func TestFriendHoldLostReplyReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	// Normal SetFriendHeld
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))

	// Verify Held
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Held, rows[0].Status)

	// Replay: run FriendHoldStep again with same parameters
	res := h.run(FriendHoldStep("amy", "glenn"))
	assert.Empty(t, res.Refused)

	// Status remains Held
	rowsAfter, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Held, rowsAfter[0].Status)
}

// TestHoldVersusTakeRaceBothOrderings tests concurrency between hold and take in both orderings:
// Ordering 1: Hold commits before Take acquires -> Take loses generation fence or replans and is refused because friend is held.
// Ordering 2: Take commits before Hold -> Hold reclaims the now-working card to Withdrawn, bumps gen, and keeps attempt count intact.
func TestHoldVersusTakeRaceBothOrderings(t *testing.T) {
	t.Parallel()

	// Ordering 1: Hold commits before Take acquires
	t.Run("HoldBeforeTake", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.startMachine()
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
		require.NoError(t, err)
		_, err = h.st.FriendBeat(h.ctx, "amy")
		require.NoError(t, err)

		h.must(AddStep(sprint.AddReq{
			Stream: "s1",
			Cards:  []sprint.CardAdd{{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"}},
		}))
		h.machine() // pump to ready
		h.machine() // deal to friend.amy ready reserve

		// Amy plans a Take based on the current generation
		snap1, gen1, err := h.st.Fenced(h.ctx, tables(sprint.Fleet, sprint.Work), nil, nil)
		require.NoError(t, err)
		seats1, err := h.st.FriendSeats(h.ctx, snap1.Now)
		require.NoError(t, err)
		snap1.Friends = seats1
		planTake := sprint.Take(snap1, sprint.TakeReq{
			Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
			As:   sprint.FriendRow("amy"),
			Gens: map[string]int{"s1-1.w1": 1},
			Who:  sprint.FriendRow("amy"),
		})
		require.NotEmpty(t, planTake.Units)

		// Before Take can Acquire, Coordinator holds Amy and commits!
		require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))

		// Now Take attempts Acquire on gen1: MUST FAIL because generation changed
		opTake, err := h.st.operation("take", "friend.amy", "op-take-stale", planTake, snap1)
		require.NoError(t, err)
		ok, err := h.st.B.Acquire(h.ctx, gen1, opTake)
		assert.False(t, ok, "Acquire must fail because fence generation advanced due to Hold")
		assert.NoError(t, err)

		// If Take replans on fresh snapshot, it sees Amy is Held and is refused
		snap2, _, err := h.st.Fenced(h.ctx, tables(sprint.Fleet, sprint.Work), nil, nil)
		require.NoError(t, err)
		seats2, err := h.st.FriendSeats(h.ctx, snap2.Now)
		require.NoError(t, err)
		snap2.Friends = seats2
		planRetry := sprint.Take(snap2, sprint.TakeReq{
			Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
			As:   sprint.FriendRow("amy"),
			Gens: map[string]int{"s1-1.w1": 1},
			Who:  sprint.FriendRow("amy"),
		})
		require.NotEmpty(t, planRetry.Refused)
		assert.Contains(t, planRetry.Refused[0].Why, "held")
	})

	// Ordering 2: Take commits before Hold
	t.Run("TakeBeforeHold", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.startMachine()
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
		require.NoError(t, err)
		_, err = h.st.FriendBeat(h.ctx, "amy")
		require.NoError(t, err)

		h.must(AddStep(sprint.AddReq{
			Stream: "s1",
			Cards:  []sprint.CardAdd{{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"}},
		}))
		h.machine() // pump to ready
		h.machine() // deal to friend.amy ready reserve

		// Take commits first
		h.must(TakeStep(sprint.TakeReq{
			Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
			As:   sprint.FriendRow("amy"),
			Gens: map[string]int{"s1-1.w1": 1},
			Who:  sprint.FriendRow("amy"),
		}))
		require.Equal(t, sprint.Working, h.snap().Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, "1", h.snap().Fleet.Card("s1-1.w1").F("attempt"))

		// Now Hold runs and commits: reclaims working card to Withdrawn, bumps gen 1 -> 2, attempt remains 1
		require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))
		c := h.snap().Fleet.Card("s1-1.w1")
		assert.Equal(t, sprint.Withdrawn, c.Col)
		assert.Equal(t, "2", c.F("gen"))
		assert.Equal(t, "1", c.F("attempt"))

		// Primary in Work table is back in Ready for redeal
		assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-1"))
	})
}

type syncRacer struct {
	Backend
	KV
	at   string
	once sync.Once
	do   func()
}

func (r *syncRacer) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if r.at == "acquire" {
		r.once.Do(r.do)
	}
	return r.Backend.Acquire(ctx, gen, op)
}

func (r *syncRacer) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if r.at == "apply "+m.Table {
		r.once.Do(r.do)
	}
	return r.Backend.Apply(ctx, m)
}

// TestSyncRemovalRaceWitness proves that a sync removal interleaved during a hold or take's
// fenced transaction/plan snapshot causes fenced verification to retry on fresh state and refuse,
// without resurrecting the removed friend or allowing stale generation claims.
func TestSyncRemovalRaceWitness(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()

	// 1. Concurrent interleaving during FriendHoldStep:
	// Sync friend amy (width 2, tiers: flash)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Tiers: []string{"flash"}}})
	require.NoError(t, err)

	other := &Store{B: h.m, Names: h.st.Names, Actor: "other", Now: h.st.Now, NewID: func() string { return "o" }, Sleep: h.st.Sleep}

	// Interleave SyncFriends removal inside Acquire of FriendHoldStep
	rHold := &syncRacer{Backend: h.m, KV: h.m, at: "acquire", do: func() {
		_, removed, _, err := other.SyncFriends(h.ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, removed)
	}}
	stHold := *h.st
	stHold.B = rHold
	stHold.root = h.m
	stHold.pinned = true

	resHold, err := stHold.Run(h.ctx, FriendHoldStep("amy", "glenn"))
	require.NoError(t, err)
	assert.Equal(t, 2, resHold.Attempts, "must retry after concurrent interleaving moved the fence")
	require.NotEmpty(t, resHold.Refused)
	assert.Contains(t, resHold.Refused[0].Why, "no friend amy on the friends table")

	// Verify roster does NOT contain amy (never resurrected with zero-width/no-tier entry)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Empty(t, rows)

	// 2. Concurrent interleaving during FriendTakeStep:
	// Re-sync friend amy
	_, _, _, err = h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)

	// Add card and stage into friend.amy ready reserve
	h.must(AddStep(sprint.AddReq{
		Stream: "s1",
		Cards: []sprint.CardAdd{
			{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"},
		},
	}))
	h.machine() // drain into work ready
	h.machine() // deal to friend.amy ready reserve
	card := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, card)
	require.Equal(t, sprint.Ready, card.Col)
	require.Equal(t, sprint.FriendRow("amy"), card.Row)
	require.Equal(t, 1, card.Int("gen"))

	// Interleave SyncFriends removal inside Acquire of FriendTakeStep
	rTake := &syncRacer{Backend: h.m, KV: h.m, at: "acquire", do: func() {
		_, removed, _, err := other.SyncFriends(h.ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, removed)
	}}
	stTake := *h.st
	stTake.B = rTake
	stTake.root = h.m
	stTake.pinned = true

	takeRes, err := stTake.Run(h.ctx, FriendTakeStep([]string{"s1-1.w1"}, map[string]int{"s1-1.w1": 1}, sprint.FriendRow("amy"), "amy"))
	require.NoError(t, err)
	assert.Equal(t, 2, takeRes.Attempts, "must retry after concurrent interleaving moved the fence")
	require.NotEmpty(t, takeRes.Refused, "take must be refused on fresh snapshot after sync removal")

	// Verify amy remains removed (not resurrected)
	rowsAfter, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Empty(t, rowsAfter)

	// Verify card was not claimed into working (no stale generation claims)
	fleetCard := h.snap().Fleet.Card("s1-1.w1")
	if fleetCard != nil {
		assert.NotEqual(t, sprint.Working, fleetCard.Col, "card must not be in working")
	}
}

// TestFriendHoldOccupiedRowQueueOnceAndReplay verifies S05 scenario (a):
// Friend hold with 1 working card: queue returns it once, replay returns nothing (empty queue).
func TestFriendHoldOccupiedRowQueueOnceAndReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()

	// 1. Sync friend amy (width 1, tiers: flash) and beat
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)

	// 2. Add card dealt to amy
	h.must(AddStep(sprint.AddReq{
		Stream: "s1",
		Cards: []sprint.CardAdd{
			{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"},
		},
	}))
	h.machine() // drain into work ready
	h.machine() // deal to friend.amy ready reserve
	cReady := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, cReady)
	require.Equal(t, sprint.Ready, cReady.Col)

	// 3. Amy takes the card: moves to working
	h.must(TakeStep(sprint.TakeReq{
		Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
		As:   sprint.FriendRow("amy"),
		Gens: map[string]int{"s1-1.w1": 1},
		Who:  sprint.FriendRow("amy"),
	}))
	cWorking := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, cWorking)
	require.Equal(t, sprint.Working, cWorking.Col)
	require.Equal(t, 1, cWorking.Int("gen"))
	require.Equal(t, sprint.Working, h.snap().StateOf("s1-1"))

	// 4. Friend hold: with 1 working card, returns working task without penalty
	resHold := h.run(FriendHoldStep("amy", "glenn"))
	require.Empty(t, resHold.Refused)

	// Fleet card is withdrawn with generation bumped 1 -> 2, attempt count preserved
	cWithdrawn := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, cWithdrawn)
	assert.Equal(t, sprint.Withdrawn, cWithdrawn.Col)
	assert.Equal(t, 2, cWithdrawn.Int("gen"))
	assert.Equal(t, "1", cWithdrawn.F("attempt"), "attempt preserved without penalty")
	assert.Empty(t, cWithdrawn.F(sprint.FieldTakeEnded))

	// Work table queue returns the primary move to ready ONCE
	q, err := h.m.QueueRead(h.ctx)
	require.NoError(t, err)
	require.Len(t, q, 1, "queue returns the primary move to ready once")
	assert.Equal(t, "s1-1", q[0].Entry.ID)
	assert.Equal(t, sprint.Ready, q[0].Entry.Move.Col)
	assert.Equal(t, "friend down", q[0].Verb)
	assert.Equal(t, h.st.Actor, q[0].Actor)

	// 5. Next machine pump drains the queue: primary is back in ready
	h.machine()
	assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-1"), "primary is back in ready in work table")
	qDrained, err := h.m.QueueRead(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, qDrained, "queue is empty after pump drain")

	// 6. Replay: run FriendHoldStep again with same parameters (duplicate hold / lost-reply replay)
	resReplay := h.run(FriendHoldStep("amy", "glenn"))
	assert.Empty(t, resReplay.Refused)
	assert.Empty(t, resReplay.Moved, "replay moves no cards on an already withdrawn row")

	// Replay returns nothing (empty queue)
	qReplay, err := h.m.QueueRead(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, qReplay, "replay must queue nothing (empty queue)")

	// Card state is unchanged: still withdrawn at gen 2, primary ready
	cAfterReplay := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Withdrawn, cAfterReplay.Col)
	assert.Equal(t, 2, cAfterReplay.Int("gen"), "generation must not be bumped again on replay")
	assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-1"))
}

// TestFriendHoldOccupiedRowCrashReplay proves that a crash during a hold on an occupied
// friend row is durably recovered, returning the primary once, and replay queues nothing.
func TestFriendHoldOccupiedRowCrashReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()

	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)

	h.must(AddStep(sprint.AddReq{
		Stream: "s1",
		Cards: []sprint.CardAdd{
			{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"},
		},
	}))
	h.machine() // drain
	h.machine() // deal
	h.must(TakeStep(sprint.TakeReq{
		Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
		As:   sprint.FriendRow("amy"),
		Gens: map[string]int{"s1-1.w1": 1},
		Who:  sprint.FriendRow("amy"),
	}))
	require.Equal(t, sprint.Working, h.snap().Fleet.Card("s1-1.w1").Col)

	// Simulate crash: Acquire fence with FriendHold on occupied row, apply manifests, do NOT Release
	snap, gen, err := h.st.Fenced(h.ctx, tables(sprint.Fleet, sprint.Work), nil, nil)
	require.NoError(t, err)
	snap.Friends = []sprint.FriendSeat{{Name: "amy", Width: 1, Status: sprint.Up, Tiers: []string{"flash"}}}
	plan := sprint.Applied(snap, sprint.FriendHold(snap, "amy", "glenn"))
	require.NotNil(t, plan.Roster)

	plan, queued := sprint.QueueOf(plan, "friend down", "glenn")
	op, err := h.st.operation("friend down", "glenn", "crash-hold-occupied-1", plan, snap)
	require.NoError(t, err)
	op.Queue = queued

	ok, err := h.st.B.Acquire(h.ctx, gen, op)
	require.True(t, ok)
	require.NoError(t, err)

	applied, _, err := h.st.apply(h.ctx, op)
	require.True(t, applied)
	require.NoError(t, err)

	// Verify fence has pending hold operation
	f, err := h.st.B.ReadFence(h.ctx)
	require.NoError(t, err)
	require.NotNil(t, f.Pending)
	require.Equal(t, "crash-hold-occupied-1", f.Pending.ID)

	// Recovery via fenced access finishes pending op
	var repaired []string
	_, _, err = h.st.Fenced(h.ctx, tables(sprint.Fleet), nil, &repaired)
	require.NoError(t, err)
	require.NotEmpty(t, repaired)

	// Check durable commit: card is withdrawn, gen is 2
	c := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, c)
	assert.Equal(t, sprint.Withdrawn, c.Col)
	assert.Equal(t, 2, c.Int("gen"))

	// Queue contains primary return once
	q, err := h.m.QueueRead(h.ctx)
	require.NoError(t, err)
	require.Len(t, q, 1, "recovery durably committed the queued work-table return once")

	// Drain queue
	h.machine()
	assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-1"))
	qDrained, _ := h.m.QueueRead(h.ctx)
	assert.Empty(t, qDrained)

	// Replay after recovery queues nothing
	resReplay := h.run(FriendHoldStep("amy", "glenn"))
	assert.Empty(t, resReplay.Refused)
	qReplay, _ := h.m.QueueRead(h.ctx)
	assert.Empty(t, qReplay, "replay after crash returns nothing (empty queue)")
}

// TestStaleHoldPlanLosingAcquireRefusedCleanly verifies S05 scenario (b):
// Stale hold plan losing Acquire is refused cleanly (does not wedge the store or deadlock).
func TestStaleHoldPlanLosingAcquireRefusedCleanly(t *testing.T) {
	t.Parallel()

	// Subtest 1: Direct backend Acquire loss by a stale hold plan
	t.Run("DirectAcquireLoss", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.startMachine()

		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
		require.NoError(t, err)
		_, err = h.st.FriendBeat(h.ctx, "amy")
		require.NoError(t, err)

		h.must(AddStep(sprint.AddReq{
			Stream: "s1",
			Cards:  []sprint.CardAdd{{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"}},
		}))
		h.machine() // pump to ready
		h.machine() // deal to friend.amy ready reserve

		// Coordinator reads snapshot at gen1 and plans a hold
		snap1, gen1, err := h.st.Fenced(h.ctx, tables(sprint.Fleet, sprint.Work), nil, nil)
		require.NoError(t, err)
		seats1, err := h.st.FriendSeats(h.ctx, snap1.Now)
		require.NoError(t, err)
		snap1.Friends = seats1

		planHold := sprint.FriendHold(snap1, "amy", "glenn")
		require.NotNil(t, planHold.Roster)
		planHold, queued := sprint.QueueOf(planHold, "friend down", "glenn")
		opHold, err := h.st.operation("friend down", "glenn", "op-hold-stale", planHold, snap1)
		require.NoError(t, err)
		opHold.Queue = queued

		// Concurrently before Hold can Acquire, Amy takes the card: moves fence generation from gen1 to gen2
		h.must(TakeStep(sprint.TakeReq{
			Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
			As:   sprint.FriendRow("amy"),
			Gens: map[string]int{"s1-1.w1": 1},
			Who:  sprint.FriendRow("amy"),
		}))
		require.Equal(t, sprint.Working, h.snap().Fleet.Card("s1-1.w1").Col)

		// Stale Hold attempts Acquire on gen1: MUST FAIL cleanly
		ok, err := h.st.B.Acquire(h.ctx, gen1, opHold)
		assert.False(t, ok, "Acquire must fail because fence generation advanced due to concurrent Take")
		assert.NoError(t, err, "Acquire failure must return nil error, not wedge the store")

		// Verify fence is clean and store is NOT wedged
		f, err := h.st.B.ReadFence(h.ctx)
		require.NoError(t, err)
		assert.Nil(t, f.Pending, "fence must have no pending operation")
		assert.Greater(t, f.Gen, gen1, "fence generation must reflect committed Take")

		// Subsequent operations succeed without deadlocking: fresh hold succeeds cleanly
		require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "glenn"))
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, sprint.Held, rows[0].Status)

		// Working card was cleanly reclaimed
		c := h.snap().Fleet.Card("s1-1.w1")
		assert.Equal(t, sprint.Withdrawn, c.Col)
		assert.Equal(t, 2, c.Int("gen"))
	})

	// Subtest 2: Interleaved Acquire race inside st.Run cleanly retries and commits
	t.Run("InterleavedRunAcquireRetry", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.startMachine()

		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Tiers: []string{"flash"}}})
		require.NoError(t, err)
		_, err = h.st.FriendBeat(h.ctx, "amy")
		require.NoError(t, err)

		h.must(AddStep(sprint.AddReq{
			Stream: "s1",
			Cards:  []sprint.CardAdd{{ID: "s1-1", Brief: "c: card 1\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\ntask 1"}},
		}))
		h.machine() // pump to ready
		h.machine() // deal to friend.amy ready reserve
		require.Equal(t, sprint.Ready, h.snap().Fleet.Card("s1-1.w1").Col)

		other := &Store{B: h.m, Names: h.st.Names, Actor: "other", Now: h.st.Now, NewID: func() string { return "o" }, Sleep: h.st.Sleep}

		// Interleave TakeStep inside Acquire of FriendHoldStep
		rHold := &syncRacer{Backend: h.m, KV: h.m, at: "acquire", do: func() {
			takeRes, err := other.Run(h.ctx, TakeStep(sprint.TakeReq{
				Sel:  sprint.Sel{IDs: []string{"s1-1.w1"}},
				As:   sprint.FriendRow("amy"),
				Gens: map[string]int{"s1-1.w1": 1},
				Who:  sprint.FriendRow("amy"),
			}))
			require.NoError(t, err)
			assert.Empty(t, takeRes.Refused)
		}}
		stHold := *h.st
		stHold.B = rHold
		stHold.root = h.m
		stHold.pinned = true

		// stHold.Run loses first Acquire, replans on fresh snapshot with card in Working, and commits cleanly
		resHold, err := stHold.Run(h.ctx, FriendHoldStep("amy", "glenn"))
		require.NoError(t, err)
		assert.Equal(t, 2, resHold.Attempts, "must retry cleanly after losing Acquire to concurrent writer")
		assert.Empty(t, resHold.Refused)

		// Amy is Held
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Held, rows[0].Status)

		// The card (which transitioned from Ready to Working during the race) was cleanly reclaimed to Withdrawn
		c := h.snap().Fleet.Card("s1-1.w1")
		assert.Equal(t, sprint.Withdrawn, c.Col)
		assert.Equal(t, 2, c.Int("gen"))

		// Primary was queued once for ready return
		q, err := h.m.QueueRead(h.ctx)
		require.NoError(t, err)
		require.Len(t, q, 1)

		h.machine() // pump drains queue
		assert.Equal(t, sprint.Ready, h.snap().StateOf("s1-1"))
	})
}

