package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A friend's health (docs/SPEC-SPRINT.md section 1, "A friend's health"; the
// model is tla/SeatHealth.tla): the coordinator's observation is fenced by
// the seat's holder and generation and by the proof's order, and the table's
// word is derived from it at every read, up or down and nothing else.

var h0 = time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)

func obs(state string, seen time.Time, gen uint64) FriendHealth {
	return FriendHealth{State: state, Seen: seen, Generation: gen}
}

// NotHealth refuses in order: a friend not on the table, no seat, the wrong
// sender, a stale generation, a proof dated after the server's clock, a proof
// not newer than the row's; and accepts the rest. Nothing is read: it is the
// fence the step applies on its own snapshot, its clock passed in.
func TestNotHealthFencesSenderGenerationAndOrder(t *testing.T) {
	t.Parallel()
	prev := obs(Up, h0, 2)
	now := h0.Add(time.Minute) // the server's clock
	tests := []struct {
		name   string
		holder string
		gen    uint64
		r      HealthReq
		want   string
	}{
		{"unknown friend", "rowan", 2, HealthReq{Friend: "zed", Who: "rowan", Obs: obs(Up, h0, 2)}, "no friend zed on the friends table"},
		{"no seat", "", 1, HealthReq{Friend: "amy", Who: "rowan", Known: true, Obs: obs(Up, h0, 1)}, "the sprint has no coordinator"},
		{"not the holder", "rowan", 2, HealthReq{Friend: "amy", Who: "stella", Known: true, Obs: obs(Up, h0, 2)}, "friend health is the seat's: rowan, not stella"},
		{"stale generation", "rowan", 3, HealthReq{Friend: "amy", Who: "rowan", Known: true, Obs: obs(Up, h0, 2)}, "the seat is rowan's at generation 3, and this observation names generation 2"},
		{"older proof", "rowan", 2, HealthReq{Friend: "amy", Who: "rowan", Known: true, Prev: prev, Obs: obs(Up, h0.Add(-time.Second), 2)}, "the row holds a proof seen at 2026-10-04T15:00:00Z, and this one's is not newer"},
		{"the same proof", "rowan", 2, HealthReq{Friend: "amy", Who: "rowan", Known: true, Prev: prev, Obs: obs(Down, h0, 2)}, "is not newer"},
		{"a proof dated after the server's clock", "rowan", 2, HealthReq{Friend: "amy", Who: "rowan", Known: true, Prev: prev, Obs: obs(Up, now.Add(time.Second), 2)}, "the proof is dated 2026-10-04T15:01:01Z, after the server's clock, 2026-10-04T15:01:00Z"},
		{"a newer proof", "rowan", 2, HealthReq{Friend: "amy", Who: "rowan", Known: true, Prev: prev, Obs: obs(Up, h0.Add(time.Second), 2)}, ""},
		{"the first proof", "rowan", 1, HealthReq{Friend: "amy", Who: "rowan", Known: true, Obs: obs(Up, h0, 1)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			why := NotHealth(tt.holder, tt.gen, now, tt.r)
			if tt.want == "" {
				assert.Empty(t, why)
				return
			}
			assert.Contains(t, why, tt.want)
		})
	}
}

// The same observation again replays: the same word, proof and generation of
// a known friend; a different word on the same proof does not (it is refused
// as not newer), nor does a first observation.
func TestHealthReplays(t *testing.T) {
	t.Parallel()
	prev := obs(Up, h0, 2)
	assert.True(t, HealthReq{Known: true, Prev: prev, Obs: obs(Up, h0, 2)}.Replays())
	assert.False(t, HealthReq{Known: true, Prev: prev, Obs: obs(Down, h0, 2)}.Replays(), "another word on the same proof")
	assert.False(t, HealthReq{Known: true, Prev: prev, Obs: obs(Up, h0, 3)}.Replays(), "another generation")
	assert.False(t, HealthReq{Known: true, Obs: obs(Up, h0, 2)}.Replays(), "no row yet")
}

// ObserveFriend on a snapshot: the plan carries the record, or the refusal.
func TestObserveFriendPlansTheRecordOrRefuses(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: h0, Coordinator: "rowan", SeatGeneration: 2}
	p := ObserveFriend(s, HealthReq{Friend: "amy", Who: "rowan", Known: true, Obs: obs(Up, h0, 2)})
	if assert.NotNil(t, p.Health) {
		assert.Equal(t, FriendHealthWrite{Friend: "amy", Health: obs(Up, h0, 2)}, *p.Health)
	}
	assert.Empty(t, p.Refused)
	p = ObserveFriend(s, HealthReq{Friend: "amy", Who: "rowan", Known: true, Obs: obs(Up, h0, 1)})
	assert.Nil(t, p.Health)
	if assert.Len(t, p.Refused, 1) {
		assert.Equal(t, "amy", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "generation 1")
	}
	// the snapshot's clock is the server's: a proof dated after it is refused
	p = ObserveFriend(s, HealthReq{Friend: "amy", Who: "rowan", Known: true, Obs: obs(Up, h0.Add(time.Second), 2)})
	assert.Nil(t, p.Health)
	if assert.Len(t, p.Refused, 1) {
		assert.Contains(t, p.Refused[0].Why, "after the server's clock")
	}
}

// The derived word: up only for an up observation under the current seat with
// a proof under ten seconds old; down at exactly ten seconds, under any other
// generation, and for every other word the row keeps (asleep is the daemon's
// word, shown as down: the owner, 2026-10-04 11:42 AM ET, "anything but up is
// down").
func TestObservedStatusIsUpOrDown(t *testing.T) {
	t.Parallel()
	up := obs(Up, h0, 2)
	assert.Equal(t, Up, ObservedStatus(up, 2, h0), "the first proof is up at once")
	assert.Equal(t, Up, ObservedStatus(up, 2, h0.Add(FriendPongWindow-time.Second)))
	assert.Equal(t, Down, ObservedStatus(up, 2, h0.Add(FriendPongWindow)), "exactly the window is down")
	assert.Equal(t, Down, ObservedStatus(up, 3, h0), "an old seat's proof never looks up under a new seat")
	assert.Equal(t, Down, ObservedStatus(up, 2, h0.Add(-time.Second)), "a proof dated after now is no proof: a negative age is not under the window")
	assert.Equal(t, Down, ObservedStatus(obs(DaemonPong, h0, 2), 2, h0), "asleep shows as down")
	assert.Equal(t, Down, ObservedStatus(obs(Down, h0, 2), 2, h0))
}

// The friends' rule over everything: the coordinator's hold wins; else her
// session's evidence alone, her fresh beat never making her up, observed or not.
func TestFriendStatusHeldThenObservationThenBeat(t *testing.T) {
	t.Parallel()
	fresh := Beat{At: h0}
	assert.Equal(t, Held, FriendStatus(FriendPresence{Held: true, Beat: fresh, Health: obs(Up, h0, 2), Generation: 2}, h0))
	assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: fresh, Health: obs(Down, h0, 2), Generation: 2}, h0), "a fresh raw beat cannot override an observed down")
	assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: fresh, Health: obs(Up, h0.Add(-FriendPongWindow), 2), Generation: 2}, h0), "no fallback to the beat once the pong is out of its window")
	assert.Equal(t, Up, FriendStatus(FriendPresence{Beat: fresh, Health: obs(Up, h0, 2), Generation: 2}, h0))
	assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: fresh, Generation: 2}, h0), "never observed: her beat is no evidence")
	assert.Equal(t, Down, FriendStatus(FriendPresence{Generation: 2}, h0), "never observed, never beaten")
}

// The seat's change carries the next generation: the snapshot's plus one.
func TestMoveSeatTakesTheNextGeneration(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: h0, Coordinator: "rowan", SeatGeneration: FirstSeatGeneration}
	p := MoveSeat(s, SeatReq{To: "stella", Who: "rowan", Reason: "handing over"})
	if assert.NotNil(t, p.Seat) {
		assert.Equal(t, uint64(2), p.Seat.Generation)
	}
	s.Coordinator, s.SeatGeneration = "stella", 2
	p = MoveSeat(s, SeatReq{To: "rowan", Who: "stella", Reason: "back"})
	if assert.NotNil(t, p.Seat) {
		assert.Equal(t, uint64(3), p.Seat.Generation, "A->B->A: A's second seat is a new generation")
	}
}

// ObservedStatus is the friends' rule over the coordinator's observation
// alone at now: up only when the observation's word is up (a wake ping her
// session answered), under the current seat generation (an old seat's proof
// never looks up under a new seat), with its proof under FriendPongWindow old
// and not dated after now (a negative age is no proof); down otherwise,
// whatever finer word the row keeps (DaemonPong, her daemon's own pong, is down).
func ObservedStatus(h FriendHealth, generation uint64, now time.Time) string {
	if session, _ := FriendSessionHeard(FriendPresence{Health: h, Generation: generation}, now); session {
		return Up
	}
	return Down
}
