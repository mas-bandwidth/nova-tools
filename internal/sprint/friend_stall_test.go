package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friend stall ladder (docs/SPEC-SPRINT.md section friend-stall-ladder-r.w1;
// the model is tla/StallLadder.tla).
//
// Invariants verified:
//   NoCardHeldPastBound: no unstarted card is held past stall_after + 4*step (taken back at rung 4).
//   NoStartedRedealt: started cards stay with friend and finish; only unstarted cards are taken back.
//   ReleasedOnlyByActivity: a friend marked down for stall is released to up only by session activity,
//     never by card progress alone.

func TestFriendStallLadderClimbsAndTakesBackUnstarted(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up}}
	dealWith(w, seats...)

	wc1 := w.s.Fleet.Card("s1-1.w1")
	wc2 := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, wc1)
	require.NotNil(t, wc2)
	require.Equal(t, Working, wc1.Col)
	require.Equal(t, Working, wc2.Col)

	// Friend amy starts s1-1 (progress stamped)
	wc1.Fields[FieldProgress] = stamp(w.s.Now)
	// s1-2 is left unstarted

	t0 := w.s.Now
	var woken []int
	wakeFn := func(friend string, rung int, d time.Duration) error {
		if friend == "amy" {
			woken = append(woken, rung)
		}
		return nil
	}

	// At t0: not stalled (< 20m)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "rung 0 has no property")
	assert.Empty(t, woken)

	// Rung 1: at t0 + 21m (between 20m and 25m)
	w.s.Now = t0.Add(21 * time.Minute)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "1", rung)
	assert.Equal(t, []int{1}, woken)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)

	// Rung 2: at t0 + 26m (between 25m and 30m)
	w.s.Now = t0.Add(26 * time.Minute)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "2", rung)
	assert.Equal(t, []int{1, 2}, woken)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)

	// Rung 3: at t0 + 31m (between 30m and 35m)
	w.s.Now = t0.Add(31 * time.Minute)
	p3 := w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "3", rung)
	var judged bool
	for _, n := range p3.Notes {
		if n.Kind == Judgment && n.Type == NStalled {
			judged = true
			assert.Contains(t, n.What, "friend amy stalled")
			assert.Contains(t, n.What, "two wakes unanswered")
		}
	}
	assert.True(t, judged, "judgment note for stall emitted at rung 3")

	// Rung 4: at t0 + 36m (between 35m and 40m)
	// Unstarted card s1-2.w1 should be taken back; started card s1-1.w1 stays!
	w.s.Now = t0.Add(36 * time.Minute)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "4", rung)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col, "unstarted card taken back")
	assert.Equal(t, Ready, w.s.Work.Card("s1-2").Col, "primary back to ready")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "started card stays with friend")

	// Rung 5: at t0 + 41m (>= 40m)
	// Friend marked down
	w.s.Now = t0.Add(41 * time.Minute)
	p5 := w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "5", rung)
	downStamp, hasDown := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.True(t, hasDown, "stall down property recorded")
	assert.NotEmpty(t, downStamp)
	require.NotNil(t, p5.Health)
	assert.Equal(t, "amy", p5.Health.Friend)
	assert.Equal(t, Down, p5.Health.Health.State)
	assert.Equal(t, "stalled", p5.Health.Health.Reason)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "started card stays even when marked down")

	// Friend session activity arrives -> released to up and ladder resets to 0!
	w.s.Now = t0.Add(42 * time.Minute)
	beats := map[string]Beat{
		"amy": {
			Friend: &FriendReport{
				Active: w.s.Now,
			},
		},
	}
	pRel := w.part(TickFriendStall, TickReq{Beats: beats, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "rung reset to 0")
	downStamp, _ = w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.Empty(t, downStamp, "stall down cleared")
	require.NotNil(t, pRel.Health)
	assert.Equal(t, "amy", pRel.Health.Friend)
	assert.Equal(t, Up, pRel.Health.Health.State)
}

func TestFriendStallLadderResetsOnProgress(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up}}
	dealWith(w, seats...)

	t0 := w.s.Now
	// Advance to rung 2 (26m)
	w.s.Now = t0.Add(26 * time.Minute)
	w.part(TickFriendStall, TickReq{Friends: seats})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	require.Equal(t, "2", rung)

	// Now friend stamps progress on the card
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)

	// Next tick sees the fresh progress: stall resets to rung 0!
	w.part(TickFriendStall, TickReq{Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "progress resets rung to 0")
}

func TestFriendStallLadderProgressDoesNotReleaseDownFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up}}
	dealWith(w, seats...)

	// Started card stays through stall ladder
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)

	t0 := w.s.Now
	// Advance to rung 5 (41m)
	w.s.Now = t0.Add(41 * time.Minute)
	p5 := w.part(TickFriendStall, TickReq{Friends: seats})
	require.NotNil(t, p5.Health)
	assert.Equal(t, Down, p5.Health.Health.State)

	// Stamping card progress resets rung, but DOES NOT release from Down (ReleasedOnlyByActivity)
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)
	pProg := w.part(TickFriendStall, TickReq{Friends: seats})
	assert.Nil(t, pProg.Health, "card progress alone does not release a down friend to up")
	_, hasDown := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.True(t, hasDown, "stall down remains")

	// Session activity releases her to Up
	w.s.Now = w.s.Now.Add(time.Minute)
	beats := map[string]Beat{
		"amy": {
			Friend: &FriendReport{
				Active: w.s.Now,
			},
		},
	}
	pRel := w.part(TickFriendStall, TickReq{Beats: beats, Friends: seats})
	require.NotNil(t, pRel.Health, "session activity releases her to up")
	assert.Equal(t, Up, pRel.Health.Health.State)
	downStamp, _ := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.Empty(t, downStamp, "stall down cleared on session activity")
}
