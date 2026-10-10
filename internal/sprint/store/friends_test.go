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
	// is carried (view coordinator reads how stale her report is), and the two facts her
	// status rests on, her daemon beating and her session's pong
	assert.Equal(t, []FriendRow{
		{Name: "amy", Width: 3, Status: sprint.Up, Evidence: "daemon up, session pong 0s ago", Beat: h.now.UTC().Truncate(time.Second), Health: &sprint.FriendHealth{State: sprint.Up, Seen: h.now, Generation: 1}},
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
// its twin (docs/SPEC-SPRINT.md section 1, "A friend's card"): in batch mode she fills up to width
// working and ready behind (DealAhead times width); in one-shot mode she gets one card at a time
// and the next only after a finish.
func TestTwinStoreDealingRespectsFriendDeliveryMode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// amy is batch mode (width 2), bob is one-shot mode (width 2, mode: one-shot)
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
	h.start("bob", 1)

	snap := h.snap()
	amyRow := sprint.FriendRow("amy")
	bobRow := sprint.FriendRow("bob")

	// Amy (batch mode): 2 working, 2 ready behind
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Working))
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Ready))

	// Bob (one-shot mode): 1 working, 0 ready behind, s1-6 and s1-7 wait ready on work table
	assert.Equal(t, 1, snap.Fleet.Count(bobRow, sprint.Working))
	assert.Equal(t, 0, snap.Fleet.Count(bobRow, sprint.Ready))
	assert.Equal(t, sprint.Ready, snap.StateOf("s1-6"))
	assert.Equal(t, sprint.Ready, snap.StateOf("s1-7"))

	// Another tick without finish: bob still holds 1 card
	h.machine()
	snap = h.snap()
	assert.Equal(t, 1, snap.Fleet.Count(bobRow, sprint.Working))
	assert.Equal(t, 0, snap.Fleet.Count(bobRow, sprint.Ready))

	// Bob finishes his card: finish step moves it out of working
	h.must(FinishStep(sprint.FinishReq{
		As: bobRow, Sel: sprint.Sel{IDs: []string{"s1-5.w1"}},
		Gens: map[string]int{"s1-5.w1": 1}, Head: "abc",
	}))
	snap = h.snap()
	assert.Equal(t, 0, snap.Fleet.Count(bobRow, sprint.Working), "no ready card auto-advances for one-shot friend")

	// Next tick deals the next card to bob, and he starts it
	h.machine()
	h.start("bob", 1)
	snap = h.snap()
	assert.Equal(t, 1, snap.Fleet.Count(bobRow, sprint.Working))
	assert.Equal(t, 0, snap.Fleet.Count(bobRow, sprint.Ready))
	assert.Equal(t, sprint.Working, snap.StateOf("s1-6"))
	assert.Equal(t, sprint.Ready, snap.StateOf("s1-7"))
}

// A friend's row switching from batch to one-shot mode through config sync preserves already-started
// work but gates queued promotion until shared work/read occupancy reaches zero (friendNext).
// A batch row width 2 has 2 Working + 2 Ready; switch it to one-shot through config sync;
// finish the first Working card: this finish must NOT start a Ready card while the other
// Working card remains active. Only when the last active card finishes does the oldest Ready
// card advance into Working.
func TestTwinStoreConfigSyncToOneShotGatesQueuedPromotionUntilOccupancyReachesZero(t *testing.T) {
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
	// Already-started work is preserved (s1-2.w1 still working)
	// but queued promotion is gated: s1-3.w1 does NOT start into working!
	assert.Equal(t, 1, snap.Fleet.Count(amyRow, sprint.Working), "only the remaining started card is working")
	assert.Equal(t, 2, snap.Fleet.Count(amyRow, sprint.Ready), "both ready cards stay ready while an active job remains")
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, sprint.Ready, snap.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, sprint.Ready, snap.Fleet.Card("s1-4.w1").Col)

	// Finish the second Working card (s1-2.w1)
	h.must(FinishStep(sprint.FinishReq{
		As: amyRow, Sel: sprint.Sel{IDs: []string{"s1-2.w1"}},
		Gens: map[string]int{"s1-2.w1": 1}, Head: "def",
	}))

	snap = h.snap()
	// Shared occupancy reached zero, so the oldest ready card (s1-3.w1) is promoted into working!
	// And s1-4.w1 remains ready (one-shot: one card at a time).
	assert.Equal(t, 1, snap.Fleet.Count(amyRow, sprint.Working), "promoted exactly one card into working")
	assert.Equal(t, 1, snap.Fleet.Count(amyRow, sprint.Ready), "remaining card stays ready")
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, sprint.Ready, snap.Fleet.Card("s1-4.w1").Col)

	// Finish the third card (s1-3.w1)
	h.must(FinishStep(sprint.FinishReq{
		As: amyRow, Sel: sprint.Sel{IDs: []string{"s1-3.w1"}},
		Gens: map[string]int{"s1-3.w1": 1}, Head: "ghi",
	}))

	snap = h.snap()
	assert.Equal(t, 1, snap.Fleet.Count(amyRow, sprint.Working))
	assert.Equal(t, 0, snap.Fleet.Count(amyRow, sprint.Ready))
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-4.w1").Col)
}

// A liveness beat that omits the running list does not know the jobs, so the
// last known ids stay (docs/SPEC-SPRINT.md, Friend presence: the up rule).
func TestABeatWithNoRunningIDsKeepsTheKnownJobs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
	require.NoError(t, err)
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"job-1", "job-2"}, Width: func() *int { n := 3; return &n }(), Active: h.now}, nil)
	require.NoError(t, err)
	h.now = h.now.Add(time.Second)
	_, _, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	beat, err := h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	require.NotNil(t, beat.Friend)
	assert.Equal(t, []string{"job-1", "job-2"}, beat.Friend.Running)
	assert.Equal(t, 3, *beat.Friend.Width)
	assert.Equal(t, h.now.Add(-time.Second), beat.Friend.Active)
	assert.Equal(t, h.now.UTC().Truncate(time.Second), beat.At)
	assert.True(t, beat.Proof.IsZero(), "liveness never invents session proof")
}

// A daemon's transition to zero running is an explicit empty list with working
// 0 (cmd/nova-friend SaveLanes). FriendBeatProof must clear the stored ids, not
// restore the finished card the way an omitted list does.
func TestADaemonsTransitionToZeroRunningClearsTheStoredList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
	require.NoError(t, err)
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"job-1", "job-2"}, Width: func() *int { n := 3; return &n }(), Active: h.now}, nil)
	require.NoError(t, err)
	h.now = h.now.Add(time.Second)
	zero := 0
	_, _, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{Running: []string{}, Working: &zero}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	beat, err := h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	require.NotNil(t, beat.Friend)
	assert.Empty(t, beat.Friend.Running, "the finished cards are not still running")
	assert.Equal(t, 0, *beat.Friend.Working)
	assert.Equal(t, 3, *beat.Friend.Width, "an omitted width still keeps the last one")
	assert.Equal(t, h.now.Add(-time.Second), beat.Friend.Active)
	assert.True(t, beat.Proof.IsZero(), "clearing the list invents no session proof")
}
