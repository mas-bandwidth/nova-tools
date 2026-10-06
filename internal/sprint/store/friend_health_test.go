package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The seat's generation and a friend's health (docs/SPEC-SPRINT.md section 1,
// "A friend's health"; the model is tla/SeatHealth.tla), on the twin store
// with the harness's clock: the generation exists from the first init with no
// seat record, every accepted seat change takes the next, unrelated steps
// keep it, and the health step is fenced by the seat in its own read.

// seat is the seat as the daemons read it.
func (h *harness) seat() SeatState {
	h.t.Helper()
	s, err := h.st.SeatState(h.ctx)
	require.NoError(h.t, err)
	return s
}

// give moves the seat from its holder to name, as the holder.
func (h *harness) give(from, to string) {
	h.t.Helper()
	res, err := h.st.Run(h.ctx, SeatStep(sprint.SeatReq{To: to, Who: from, Reason: "handover"}))
	require.NoError(h.t, err)
	require.Empty(h.t, res.Refused, "the seat moves")
}

// health is one observation by who, at the harness's clock.
func (h *harness) health(friend, who, state string, seen time.Time, gen uint64) (sprint.FriendHealth, string, bool, error) {
	h.t.Helper()
	return h.st.FriendHealth(h.ctx, friend, who, sprint.FriendHealth{State: state, Seen: seen, Generation: gen}, "")
}

func (h *harness) friendStatus(name string) string {
	h.t.Helper()
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(h.t, err)
	for _, r := range rows {
		if r.Name == name {
			return r.Status
		}
	}
	require.Fail(h.t, "no row for "+name)
	return ""
}

// The initial seat has generation 1 and no record; a step that is not the
// seat's keeps it; each accepted handover takes the next; the same op again
// is the recorded result, not a second increment.
func TestSeatGenerationFromInitThroughHandovers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	assert.Equal(t, SeatState{Holder: "tester", Epoch: 0, Generation: 1}, h.seat(), "the first init's seat, no record")
	_, moved, err := h.st.Seat(h.ctx)
	require.NoError(t, err)
	assert.False(t, moved)

	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	assert.Equal(t, uint64(1), h.seat().Generation, "an unrelated write keeps the generation")

	step := SeatStep(sprint.SeatReq{To: "stella", Who: "tester", Reason: "handover"})
	step.CallerOp = "seat-1"
	res, err := h.st.Run(h.ctx, step)
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	assert.Equal(t, SeatState{Holder: "stella", Generation: 2}, h.seat())
	res, err = h.st.Run(h.ctx, step)
	require.NoError(t, err)
	assert.True(t, res.Replay, "the same op again replays")
	assert.Equal(t, uint64(2), h.seat().Generation, "no double increment")

	h.give("stella", "tester")
	assert.Equal(t, SeatState{Holder: "tester", Generation: 3}, h.seat(), "A->B->A: A's second seat is generation 3")
	last, moved, err := h.st.Seat(h.ctx)
	require.NoError(t, err)
	assert.True(t, moved)
	assert.Equal(t, uint64(3), last.Generation, "the record carries it")
}

// A first valid observation makes the friend up at once; at exactly ten
// seconds with no newer proof she is down; a newer proof restores her.
func TestFirstProofIsUpAndTenSecondsWithoutOneIsDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	h.daemon("amy") // her daemon beats every step: the half of up these tests do not vary
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "never observed: her daemon beating alone is not up")

	rec, status, replayed, err := h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, sprint.Up, status)
	assert.Equal(t, sprint.FriendHealth{State: sprint.Up, Seen: h.now, Generation: 1}, rec)
	assert.Equal(t, sprint.Up, h.friendStatus("amy"))

	h.tick(sprint.FriendPongWindow - time.Second)
	assert.Equal(t, sprint.Up, h.friendStatus("amy"))
	h.tick(time.Second)
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "exactly ten seconds: down")

	_, status, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	assert.Equal(t, sprint.Up, status, "a fresh proof restores her")
	assert.Equal(t, sprint.Up, h.friendStatus("amy"))
}

// A proof dated after the server's clock is refused and writes nothing (Stella's
// review of PR 5305: a --seen ahead held a friend up until it plus ten seconds,
// and the order then refused every real observation before it); one at the
// server's clock is accepted, and a later real one after it.
func TestAProofDatedAfterTheServersClockIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	h.daemon("amy") // her daemon beats every step: the half of up these tests do not vary
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now.Add(time.Hour), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after the server's clock")
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "a refusal writes nothing")
	_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
	require.NoError(t, err)
	assert.False(t, ok, "no record")

	_, status, _, err := h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err, "a proof at the server's clock")
	assert.Equal(t, sprint.Up, status)
	h.tick(time.Second)
	_, status, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err, "the next real proof is newer, never refused by a future one")
	assert.Equal(t, sprint.Up, status)
}

// The same observation again is answered as recorded and writes nothing; an
// older or equal proof with another word is refused and writes nothing; a
// fresh raw beat never overrides an observed down; the coordinator's hold
// wins over everything; asleep is kept on the row and shown as down.
func TestHealthReplayOrderBeatAndHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	h.daemon("amy") // her daemon beats every step: the half of up these tests do not vary
	seen := h.now
	_, _, _, err = h.health("amy", "tester", sprint.Up, seen, 1)
	require.NoError(t, err)

	h.tick(5 * time.Second)
	rec, status, replayed, err := h.health("amy", "tester", sprint.Up, seen, 1)
	require.NoError(t, err)
	assert.True(t, replayed, "the same proof again")
	assert.Equal(t, seen, rec.Seen, "the row's proof, not renewed")
	assert.Equal(t, sprint.Up, status)
	h.tick(sprint.FriendPongWindow - 5*time.Second)
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "the replay earned no second window")

	_, _, _, err = h.health("amy", "tester", sprint.Down, seen.Add(-time.Second), 1)
	require.Error(t, err, "an older proof is refused")
	assert.Contains(t, err.Error(), "is not newer")
	_, _, _, err = h.health("amy", "tester", sprint.Down, seen, 1)
	require.Error(t, err, "the same proof with another word is refused")

	// an explicit down, then her own beat: no fallback to the beat once observed
	_, status, _, err = h.health("amy", "tester", sprint.Down, h.now, 1)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, status)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "a fresh raw beat cannot make an observed-down friend up")

	// asleep: the daemon's word, kept on the row, shown as down
	h.tick(time.Second)
	_, status, _, err = h.health("amy", "tester", sprint.DaemonPong, h.now, 1)
	require.NoError(t, err)
	assert.Equal(t, sprint.Down, status)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Down, rows[0].Status)
	if assert.NotNil(t, rows[0].Health) {
		assert.Equal(t, sprint.DaemonPong, rows[0].Health.State, "the finer word stays on the row for the daemon")
	}

	// held wins, whatever the observation says, and friend up lifts it
	h.tick(time.Second)
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "tester", "opus rate limited", h.now.Add(time.Hour), 0))
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	assert.Equal(t, sprint.Held, rows[0].Status)
	assert.Equal(t, "opus rate limited", rows[0].Reason, "the hold's reason is on the row")
	assert.Equal(t, h.now.Add(time.Hour).UTC(), rows[0].Until)
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", false, "tester", "", time.Time{}, 0))
	assert.Equal(t, sprint.Up, h.friendStatus("amy"))
}

// The fence: an observation names the seat's generation and comes from its
// holder. After A->B->A, A's observation at its first generation is refused
// and writes nothing; one at the current generation is accepted; the old
// seat's record shows the friend down under the new seat until then; B,
// no longer the holder, is refused.
func TestHealthIsFencedBySeatHolderAndGeneration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	h.daemon("amy") // her daemon beats every step: the half of up these tests do not vary
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)

	h.give("tester", "stella")
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "an old seat's proof never looks up under a new seat")
	h.tick(time.Second)
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.Error(t, err, "the old holder at the old generation")
	assert.Contains(t, err.Error(), "friend health is the seat's: stella, not tester")
	_, _, _, err = h.health("amy", "stella", sprint.Up, h.now, 1)
	require.Error(t, err, "the holder at a stale generation")
	assert.Contains(t, err.Error(), "the seat is stella's at generation 2, and this observation names generation 1")
	assert.Equal(t, sprint.Down, h.friendStatus("amy"), "a refusal writes nothing")
	_, status, _, err := h.health("amy", "stella", sprint.Up, h.now, 2)
	require.NoError(t, err)
	assert.Equal(t, sprint.Up, status)

	h.give("stella", "tester")
	h.tick(time.Second)
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.Error(t, err, "A's first generation is stale after A->B->A")
	assert.Contains(t, err.Error(), "generation 3")
	_, _, _, err = h.health("amy", "stella", sprint.Up, h.now, 3)
	require.Error(t, err, "B is no longer the holder")
	_, status, _, err = h.health("amy", "tester", sprint.Up, h.now, 3)
	require.NoError(t, err)
	assert.Equal(t, sprint.Up, status)

	// an unknown friend, and a store with no coordinator
	_, _, _, err = h.health("zed", "tester", sprint.Up, h.now, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no friend zed")
	require.NoError(t, h.m.SetCoordinator(context.Background(), ""))
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now.Add(time.Second), 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no coordinator")
}

// The health record goes with the friend: friend sync taking her off the
// roster deletes it, and teardown's keys name it.
func TestHealthRecordIsRemovedWithTheFriend(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
	require.NoError(t, err)
	assert.True(t, ok, "the record is written by the step's commit")
	_, removed, _, err := h.st.SyncFriends(h.ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"amy"}, removed)
	_, ok, err = h.m.GetKey(h.ctx, friendHealthKey("amy"))
	require.NoError(t, err)
	assert.False(t, ok, "taken off with her")
	keys := TeardownKeys(h.st.Names, nil, Epochs{Friends: []string{"bob"}})
	assert.Contains(t, keys, h.st.Names.Key(friendHealthKey("bob")))
}
