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

// A beat that says her named session is gone (friend beat --target-invalid) reads
// target-invalid on her row, its own word and never down, even with a session pong in
// its window: she is not up, the seat's why names the rebind, and her row's session
// rides the roster (friend sync) for her beat to answer. A beat without it clears it;
// the coordinator's hold comes first.
func TestAGoneTargetReadsTargetInvalidOnHerRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "stella", Width: 2, Session: "019a-new"}})
	require.NoError(t, err)
	spec, err := h.st.FriendSpecOf(h.ctx, "stella")
	require.NoError(t, err)
	assert.Equal(t, "019a-new", spec.Session)
	_, _, _, err = h.health("stella", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	_, err = h.st.FriendBeatGone(h.ctx, "stella", sprint.FriendReport{}, nil, time.Time{}, &GoneTarget{Session: "01a10e84", State: "archived: the thread's rollout is under archived_sessions"})
	require.NoError(t, err)

	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, TargetInvalid, rows[0].Status)
	assert.NotEqual(t, sprint.Down, rows[0].Status)
	assert.Contains(t, rows[0].Evidence, "01a10e84")
	assert.Contains(t, rows[0].Evidence, "nova-friend rebind --as stella --session <id>")
	seats, err := h.st.FriendSeats(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, TargetInvalid, seats[0].Status, "nothing is dealt to her")
	assert.Contains(t, seats[0].Why, "target is invalid")

	_, err = h.st.FriendBeat(h.ctx, "stella")
	require.NoError(t, err)
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Up, rows[0].Status, "a beat without it clears it")

	_, err = h.st.FriendBeatGone(h.ctx, "stella", sprint.FriendReport{}, nil, time.Time{}, &GoneTarget{Session: "01a10e84", State: "archived"})
	require.NoError(t, err)
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "stella", true, "c", "", time.Time{}, 0))
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Held, rows[0].Status, "the hold comes first")
}

// The beat record keeps her proof on its outer pong, and that field owns the json
// name the beat's Proof also uses. Her row reads the beat, so an answered check is up.
func TestAProvedBeatReadsUpFromTheRecordsPong(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
	require.NoError(t, err)
	_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Check: "n1", Pong: "n1"})
	require.NoError(t, err)
	require.True(t, proof.Proved)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Up, rows[0].Status)
	assert.Equal(t, proof.Proof, rows[0].Proof)
	assert.Contains(t, rows[0].Evidence, "session proof")
}

// A rebind that changes a session already on her roster drops that session
// proof. Her row is not up on the old answer before a check through the new
// session, and a pong of the old nonce does not restore it.
func TestAReboundSessionDropsTheOldProofUntilANewCheck(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_old"}})
	require.NoError(t, err)
	_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Check: "n1", Pong: "n1"})
	require.NoError(t, err)
	require.True(t, proof.Proved)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Up, rows[0].Status)
	assert.False(t, rows[0].Proof.IsZero())

	_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_new"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"amy"}, updated)
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, rows[0].Status, "the old session proof is not evidence after rebind")
	assert.True(t, rows[0].Proof.IsZero())

	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Pong: "n1"})
	require.NoError(t, err)
	assert.False(t, proof.Proved, "the nonce the old session was asked is not evidence")
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, rows[0].Status)

	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r2", Check: "n2", Pong: "n2"})
	require.NoError(t, err)
	require.True(t, proof.Proved)
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Up, rows[0].Status, "a check through the new session proves her")
}

// A rebind immediately after a health pong, and immediately after a finish,
// drops those signals with the beat's proof. FriendEvidence reads either as
// up inside its window, so clearing only the beat record's pong would leave
// her up and eligible before the new session answers a check.
func TestAReboundDropsTheHealthPongAndTheFinish(t *testing.T) {
	t.Parallel()
	t.Run("health pong", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_old"}})
		require.NoError(t, err)
		_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
		require.NoError(t, err)
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, sprint.Up, rows[0].Status)
		assert.Equal(t, "session pong 0s ago", rows[0].Evidence)

		_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_new"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, updated)
		_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
		require.NoError(t, err)
		assert.False(t, ok, "the old session pong is dropped")
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, rows[0].Status, "a health pong of the old session is not up")
		assert.NotContains(t, rows[0].Evidence, "session pong")
		seats, err := h.st.FriendSeats(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, seats[0].Status, "not eligible for new work")

		_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Check: "n1", Pong: "n1"})
		require.NoError(t, err)
		require.True(t, proof.Proved)
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Up, rows[0].Status, "a check through the new session proves her")
	})
	t.Run("finish", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_old"}})
		require.NoError(t, err)
		require.NoError(t, h.st.FriendFinished(h.ctx, "amy", h.now))
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, sprint.Up, rows[0].Status)
		assert.Equal(t, "finish 0s ago", rows[0].Evidence)

		_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_new"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, updated)
		_, ok, err := h.m.GetKey(h.ctx, friendFinishKey("amy"))
		require.NoError(t, err)
		assert.False(t, ok, "the old finish is dropped")
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, rows[0].Status, "a finish of the old session is not up")
		assert.NotContains(t, rows[0].Evidence, "finish 0s ago")
		seats, err := h.st.FriendSeats(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, seats[0].Status, "not eligible for new work")
	})
}

// A legacy roster row has an empty session. Binding it to a named session is
// a target change when old evidence exists: the health pong, the finish and
// the beat proof were not answered through the new session, so the row is
// not up, and not eligible, before that session answers a fresh check.
func TestBindingAnEmptySessionDropsOldEvidence(t *testing.T) {
	t.Parallel()
	t.Run("health pong", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
		require.NoError(t, err)
		_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
		require.NoError(t, err)
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, sprint.Up, rows[0].Status)
		assert.Equal(t, "session pong 0s ago", rows[0].Evidence)

		_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_new"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, updated)
		_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
		require.NoError(t, err)
		assert.False(t, ok, "the old session pong is dropped")
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, rows[0].Status, "a health pong from before the bind is not up")
		assert.NotContains(t, rows[0].Evidence, "session pong")
		seats, err := h.st.FriendSeats(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, seats[0].Status, "not eligible for new work")
	})
	t.Run("finish", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
		require.NoError(t, err)
		require.NoError(t, h.st.FriendFinished(h.ctx, "amy", h.now))
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, sprint.Up, rows[0].Status)
		assert.Equal(t, "finish 0s ago", rows[0].Evidence)

		_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_new"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, updated)
		_, ok, err := h.m.GetKey(h.ctx, friendFinishKey("amy"))
		require.NoError(t, err)
		assert.False(t, ok, "the old finish is dropped")
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, rows[0].Status, "a finish from before the bind is not up")
		assert.NotContains(t, rows[0].Evidence, "finish 0s ago")
		seats, err := h.st.FriendSeats(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, seats[0].Status, "not eligible for new work")
	})
	t.Run("proof", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
		require.NoError(t, err)
		_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Check: "n1", Pong: "n1"})
		require.NoError(t, err)
		require.True(t, proof.Proved)
		rows, err := h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, sprint.Up, rows[0].Status)
		assert.False(t, rows[0].Proof.IsZero())

		_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Session: "ses_new"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"amy"}, updated)
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, rows[0].Status, "an empty-session proof is not evidence after the bind")
		assert.True(t, rows[0].Proof.IsZero())
		seats, err := h.st.FriendSeats(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, seats[0].Status, "not eligible for new work")

		_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Pong: "n1"})
		require.NoError(t, err)
		assert.False(t, proof.Proved, "the nonce asked before the bind is not evidence")
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, rows[0].Status)

		_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r2", Check: "n2", Pong: "n2"})
		require.NoError(t, err)
		require.True(t, proof.Proved)
		rows, err = h.st.FriendRows(h.ctx, h.now)
		require.NoError(t, err)
		assert.Equal(t, sprint.Up, rows[0].Status, "a check through the new session proves her")
	})
}

// FriendBeatFull must not copy an empty-session proof onto a named roster
// session. While the roster still names no session, a later beat keeps that
// proof, so a deploy does not put an unbound friend down. Naming the session
// on the row, even before friend sync drops the record, makes the next beat
// drop the proof and the nonce the old session was asked.
func TestFriendBeatFullDoesNotKeepAnEmptySessionProofOnANamedRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
	require.NoError(t, err)
	_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Check: "n1", Pong: "n1"})
	require.NoError(t, err)
	require.True(t, proof.Proved)

	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Up, rows[0].Status, "an unbound row keeps the empty-session proof")
	assert.False(t, rows[0].Proof.IsZero())

	r, kv, err := h.st.roster(h.ctx)
	require.NoError(t, err)
	e := r["amy"]
	e.Session = "ses_new"
	r["amy"] = e
	require.NoError(t, putRoster(h.ctx, kv, r))

	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.False(t, proof.Proved)
	assert.True(t, proof.Proof.IsZero(), "the empty-session proof is not copied onto the named session")
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, rows[0].Status)
	assert.True(t, rows[0].Proof.IsZero())

	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{Run: "r1", Pong: "n1"})
	require.NoError(t, err)
	assert.False(t, proof.Proved, "the nonce asked under the empty session is not evidence")
}
